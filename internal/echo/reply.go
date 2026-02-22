package echo

import (
	"bytes"
	"context"
	"crypto/tls"
	"fmt"
	stdhtml "html"
	"io"
	"log"
	"mime"
	"net"
	"regexp"
	"sort"
	"strings"
	"time"

	_ "github.com/emersion/go-message/charset"
	"github.com/emersion/go-message/mail"
	"github.com/emersion/go-smtp"

	"github.com/danthegoodman1/smtp_echo/internal/config"
)

type Replier struct {
	hostname    string
	fromAddress string
	mailFrom    string
	fromName    string
	logger      *log.Logger
	deliverFn   func(ctx context.Context, to string, message []byte) error
}

func NewReplier(cfg config.Config, logger *log.Logger) *Replier {
	replier := &Replier{
		hostname:    cfg.Hostname,
		fromAddress: cfg.Reply.FromAddress,
		mailFrom:    cfg.Reply.MailFrom,
		fromName:    cfg.Reply.FromName,
		logger:      logger,
	}
	replier.deliverFn = replier.deliverDirect
	return replier
}

func (r *Replier) Echo(ctx context.Context, msg InboundMessage) error {
	reader, err := mail.CreateReader(bytes.NewReader(msg.Data))
	if err != nil {
		return fmt.Errorf("parse inbound message: %w", err)
	}

	recipient, err := selectReplyRecipient(msg.EnvelopeFrom, reader.Header)
	if err != nil {
		return err
	}

	body, err := readReplyBody(reader, msg.Data)
	if err != nil {
		return err
	}

	meta := extractThreadMetadata(reader.Header)
	replyMessage, err := r.buildReplyMessage(recipient, body, meta)
	if err != nil {
		return err
	}

	if err := r.deliverFn(ctx, recipient, replyMessage); err != nil {
		return err
	}

	if r.logger != nil {
		r.logger.Printf("sent echo reply to=%q bytes=%d", recipient, len(replyMessage))
	}

	return nil
}

type threadMetadata struct {
	Subject    string
	MessageID  string
	References []string
}

type replyBody struct {
	Plain string
	HTML  string
}

func extractThreadMetadata(header mail.Header) threadMetadata {
	meta := threadMetadata{}

	subject, err := header.Subject()
	if err == nil {
		meta.Subject = subject
	}

	messageID, err := header.MessageID()
	if err == nil {
		meta.MessageID = messageID
	}

	references, err := header.MsgIDList("References")
	if err == nil {
		meta.References = references
	}

	if meta.MessageID != "" && !containsString(meta.References, meta.MessageID) {
		meta.References = append(meta.References, meta.MessageID)
	}

	return meta
}

func selectReplyRecipient(envelopeFrom string, header mail.Header) (string, error) {
	if envelopeRecipient := normalizeRecipientAddress(envelopeFrom); envelopeRecipient != "" {
		return envelopeRecipient, nil
	}

	for _, key := range []string{"Reply-To", "From"} {
		addresses, err := header.AddressList(key)
		if err != nil {
			continue
		}
		for _, address := range addresses {
			if address.Address != "" {
				return address.Address, nil
			}
		}
	}

	return "", fmt.Errorf("unable to determine reply recipient")
}

func normalizeRecipientAddress(value string) string {
	trimmed := strings.TrimSpace(value)
	trimmed = strings.TrimPrefix(trimmed, "<")
	trimmed = strings.TrimSuffix(trimmed, ">")
	if trimmed == "" {
		return ""
	}

	parsedAddress, err := mail.ParseAddress(trimmed)
	if err == nil {
		return parsedAddress.Address
	}

	if strings.Count(trimmed, "@") == 1 && !strings.ContainsAny(trimmed, " \t") {
		return trimmed
	}

	return ""
}

func readReplyBody(reader *mail.Reader, originalData []byte) (replyBody, error) {
	var plainSegments []string
	var htmlSegments []string

	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return replyBody{}, fmt.Errorf("read message part: %w", err)
		}

		contentDisposition := strings.ToLower(part.Header.Get("Content-Disposition"))
		if strings.HasPrefix(contentDisposition, "attachment") {
			continue
		}

		partBytes, err := io.ReadAll(part.Body)
		if err != nil {
			return replyBody{}, fmt.Errorf("read message part body: %w", err)
		}
		if len(partBytes) == 0 {
			continue
		}

		switch normalizeMediaType(part.Header.Get("Content-Type")) {
		case "", "text/plain":
			plainSegments = append(plainSegments, string(partBytes))
		case "text/html":
			htmlSegments = append(htmlSegments, string(partBytes))
		}
	}

	body := replyBody{
		Plain: strings.Join(plainSegments, "\n\n"),
		HTML:  strings.Join(htmlSegments, "\n\n"),
	}

	if body.Plain == "" && body.HTML == "" {
		rawBody := extractRawBody(originalData)
		switch normalizeMediaType(reader.Header.Get("Content-Type")) {
		case "text/html":
			body.HTML = rawBody
			body.Plain = htmlToText(rawBody)
		default:
			body.Plain = rawBody
		}
	}

	if body.Plain == "" && body.HTML != "" {
		body.Plain = htmlToText(body.HTML)
	}

	return body, nil
}

func normalizeMediaType(contentType string) string {
	if contentType == "" {
		return ""
	}

	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	}
	return strings.ToLower(strings.TrimSpace(mediaType))
}

var htmlTagPattern = regexp.MustCompile(`(?s)<[^>]*>`)

func htmlToText(input string) string {
	withoutTags := htmlTagPattern.ReplaceAllString(input, " ")
	unescaped := stdhtml.UnescapeString(withoutTags)
	return strings.TrimSpace(strings.Join(strings.Fields(unescaped), " "))
}

func extractRawBody(data []byte) string {
	if idx := bytes.Index(data, []byte("\r\n\r\n")); idx >= 0 {
		return string(data[idx+4:])
	}
	if idx := bytes.Index(data, []byte("\n\n")); idx >= 0 {
		return string(data[idx+2:])
	}
	return ""
}

func (r *Replier) buildReplyMessage(recipient string, body replyBody, meta threadMetadata) ([]byte, error) {
	fromAddress, err := mail.ParseAddress(r.fromAddress)
	if err != nil {
		return nil, fmt.Errorf("invalid configured from_address: %w", err)
	}
	if r.fromName != "" {
		fromAddress.Name = r.fromName
	}

	parsedRecipient, err := mail.ParseAddress(recipient)
	if err != nil {
		return nil, fmt.Errorf("invalid recipient address: %w", err)
	}

	subject := normalizeReplySubject(meta.Subject)
	if subject == "" {
		subject = "Re:"
	}

	var header mail.Header
	header.SetDate(time.Now().UTC())
	header.SetSubject(subject)
	header.SetAddressList("From", []*mail.Address{fromAddress})
	header.SetAddressList("To", []*mail.Address{{Address: parsedRecipient.Address}})
	if meta.MessageID != "" {
		header.SetMsgIDList("In-Reply-To", []string{meta.MessageID})
	}
	if len(meta.References) > 0 {
		header.SetMsgIDList("References", meta.References)
	}

	if err := header.GenerateMessageIDWithHostname(r.hostname); err != nil {
		if generateErr := header.GenerateMessageID(); generateErr != nil {
			return nil, fmt.Errorf("generate message-id: %w", generateErr)
		}
	}

	var buf bytes.Buffer
	plainBody := body.Plain
	htmlBody := body.HTML
	if plainBody == "" && htmlBody == "" {
		plainBody = "\n"
	}

	if htmlBody == "" {
		inlineWriter, err := mail.CreateSingleInlineWriter(&buf, header)
		if err != nil {
			return nil, fmt.Errorf("create reply writer: %w", err)
		}
		if _, err := io.WriteString(inlineWriter, plainBody); err != nil {
			return nil, fmt.Errorf("write reply body: %w", err)
		}
		if err := inlineWriter.Close(); err != nil {
			return nil, fmt.Errorf("close reply writer: %w", err)
		}
		return buf.Bytes(), nil
	}

	if plainBody == "" {
		plainBody = htmlToText(htmlBody)
		if plainBody == "" {
			plainBody = "\n"
		}
	}

	writer, err := mail.CreateWriter(&buf, header)
	if err != nil {
		return nil, fmt.Errorf("create multipart reply writer: %w", err)
	}

	inlineWriter, err := writer.CreateInline()
	if err != nil {
		return nil, fmt.Errorf("create inline writer: %w", err)
	}

	var plainHeader mail.InlineHeader
	plainHeader.SetContentType("text/plain", map[string]string{"charset": "utf-8"})
	plainPart, err := inlineWriter.CreatePart(plainHeader)
	if err != nil {
		return nil, fmt.Errorf("create plain part: %w", err)
	}
	if _, err := io.WriteString(plainPart, plainBody); err != nil {
		return nil, fmt.Errorf("write plain part: %w", err)
	}
	if err := plainPart.Close(); err != nil {
		return nil, fmt.Errorf("close plain part: %w", err)
	}

	var htmlHeader mail.InlineHeader
	htmlHeader.SetContentType("text/html", map[string]string{"charset": "utf-8"})
	htmlPart, err := inlineWriter.CreatePart(htmlHeader)
	if err != nil {
		return nil, fmt.Errorf("create html part: %w", err)
	}
	if _, err := io.WriteString(htmlPart, htmlBody); err != nil {
		return nil, fmt.Errorf("write html part: %w", err)
	}
	if err := htmlPart.Close(); err != nil {
		return nil, fmt.Errorf("close html part: %w", err)
	}

	if err := inlineWriter.Close(); err != nil {
		return nil, fmt.Errorf("close inline writer: %w", err)
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("close multipart writer: %w", err)
	}

	return buf.Bytes(), nil
}

func normalizeReplySubject(subject string) string {
	trimmed := strings.TrimSpace(subject)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(trimmed), "re:") {
		return trimmed
	}
	return "Re: " + trimmed
}

func (r *Replier) deliverDirect(ctx context.Context, to string, message []byte) error {
	parsedRecipient, err := mail.ParseAddress(to)
	if err != nil {
		return fmt.Errorf("parse recipient: %w", err)
	}

	domain, err := addressDomain(parsedRecipient.Address)
	if err != nil {
		return err
	}

	mxRecords, lookupErr := net.DefaultResolver.LookupMX(ctx, domain)
	targetHosts := make([]string, 0, len(mxRecords))
	if lookupErr == nil && len(mxRecords) > 0 {
		sort.Slice(mxRecords, func(i, j int) bool {
			return mxRecords[i].Pref < mxRecords[j].Pref
		})
		for _, mxRecord := range mxRecords {
			targetHosts = append(targetHosts, normalizeMXHost(mxRecord.Host))
		}
	} else {
		targetHosts = append(targetHosts, domain)
	}

	var attemptErrors []string
	if lookupErr != nil {
		attemptErrors = append(attemptErrors, "mx lookup: "+lookupErr.Error())
	}

	for _, host := range targetHosts {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err := r.sendToHost(host, parsedRecipient.Address, message); err != nil {
			attemptErrors = append(attemptErrors, fmt.Sprintf("%s: %v", host, err))
			continue
		}
		return nil
	}

	return fmt.Errorf("delivery failed for %s: %s", parsedRecipient.Address, strings.Join(attemptErrors, " | "))
}

func normalizeMXHost(host string) string {
	return strings.TrimSuffix(host, ".")
}

func addressDomain(address string) (string, error) {
	atIndex := strings.LastIndex(address, "@")
	if atIndex <= 0 || atIndex == len(address)-1 {
		return "", fmt.Errorf("recipient address missing domain: %q", address)
	}
	return address[atIndex+1:], nil
}

func (r *Replier) sendToHost(host string, recipient string, message []byte) error {
	address := net.JoinHostPort(host, "25")

	client, tlsEnabled, err := dialSMTPClient(address, host)
	if err != nil {
		return err
	}
	defer client.Close()

	if !tlsEnabled && r.hostname != "" {
		if err := client.Hello(r.hostname); err != nil {
			return fmt.Errorf("helo/ehlo failed: %w", err)
		}
	}

	if err := client.SendMail(r.mailFrom, []string{recipient}, bytes.NewReader(message)); err != nil {
		return fmt.Errorf("send mail: %w", err)
	}

	if err := client.Quit(); err != nil {
		return fmt.Errorf("quit smtp session: %w", err)
	}

	return nil
}

func dialSMTPClient(address string, host string) (*smtp.Client, bool, error) {
	tlsConfig := &tls.Config{
		ServerName: host,
		MinVersion: tls.VersionTLS12,
	}

	tlsClient, tlsErr := smtp.DialStartTLS(address, tlsConfig)
	if tlsErr == nil {
		return tlsClient, true, nil
	}

	plainClient, plainErr := smtp.Dial(address)
	if plainErr != nil {
		return nil, false, fmt.Errorf("starttls failed (%v), plain failed (%w)", tlsErr, plainErr)
	}

	return plainClient, false, nil
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
