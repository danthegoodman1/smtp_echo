package echo

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"io"
	"log"
	"os"
	"strings"
	"testing"

	"github.com/emersion/go-message/mail"

	"github.com/danthegoodman1/smtp_echo/internal/config"
)

func TestReplierEcho_EnvelopeRecipientAndThreadHeaders(t *testing.T) {
	cfg := config.Config{
		Hostname: "echo.example.com",
		Reply: config.ReplyConfig{
			FromAddress: "echo@example.com",
			MailFrom:    "bounce@example.com",
			FromName:    "Echo Bot",
		},
	}

	replier, err := NewReplier(cfg, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatalf("NewReplier() error = %v", err)
	}

	var deliveredTo string
	var deliveredMessage []byte
	replier.deliverFn = func(_ context.Context, to string, message []byte) error {
		deliveredTo = to
		deliveredMessage = append([]byte(nil), message...)
		return nil
	}

	inbound := strings.Join([]string{
		"From: Header Sender <header-sender@example.net>",
		"Reply-To: reply-to@example.net",
		"To: echo@example.com",
		"Subject: Hello",
		"Message-ID: <message-1@example.net>",
		"References: <root@example.net>",
		"",
		"Body line 1",
		"Body line 2",
		"",
	}, "\r\n")

	err = replier.Echo(context.Background(), InboundMessage{
		EnvelopeFrom: "envelope-sender@example.net",
		Data:         []byte(inbound),
	})
	if err != nil {
		t.Fatalf("Echo() error = %v", err)
	}

	if deliveredTo != "envelope-sender@example.net" {
		t.Fatalf("deliveredTo = %q, want %q", deliveredTo, "envelope-sender@example.net")
	}

	reader, err := mail.CreateReader(bytes.NewReader(deliveredMessage))
	if err != nil {
		t.Fatalf("CreateReader() error = %v", err)
	}

	toAddresses, err := reader.Header.AddressList("To")
	if err != nil || len(toAddresses) != 1 {
		t.Fatalf("reply To header parse failed: %v", err)
	}
	if toAddresses[0].Address != "envelope-sender@example.net" {
		t.Fatalf("To = %q, want %q", toAddresses[0].Address, "envelope-sender@example.net")
	}

	subject, err := reader.Header.Subject()
	if err != nil {
		t.Fatalf("Subject() error = %v", err)
	}
	if subject != "Re: Hello" {
		t.Fatalf("Subject = %q, want %q", subject, "Re: Hello")
	}

	inReplyTo, err := reader.Header.MsgIDList("In-Reply-To")
	if err != nil || len(inReplyTo) != 1 {
		t.Fatalf("In-Reply-To parse failed: %v", err)
	}
	if inReplyTo[0] != "message-1@example.net" {
		t.Fatalf("In-Reply-To = %q, want %q", inReplyTo[0], "message-1@example.net")
	}

	references, err := reader.Header.MsgIDList("References")
	if err != nil {
		t.Fatalf("References parse failed: %v", err)
	}
	if len(references) != 2 || references[0] != "root@example.net" || references[1] != "message-1@example.net" {
		t.Fatalf("References = %#v, want [root@example.net message-1@example.net]", references)
	}

	body, err := readReplyBody(reader, deliveredMessage)
	if err != nil {
		t.Fatalf("readReplyBody() error = %v", err)
	}
	if !strings.Contains(body.Plain, "Body line 1") || !strings.Contains(body.Plain, "Body line 2") {
		t.Fatalf("reply plain body does not include expected content, got: %q", body.Plain)
	}
}

func TestSelectReplyRecipient_FallbackOrder(t *testing.T) {
	makeHeader := func(raw string) mail.Header {
		reader, err := mail.CreateReader(strings.NewReader(raw))
		if err != nil {
			t.Fatalf("CreateReader() error = %v", err)
		}
		return reader.Header
	}

	tests := []struct {
		name         string
		envelopeFrom string
		rawMessage   string
		want         string
	}{
		{
			name:         "envelope sender wins",
			envelopeFrom: "env@example.net",
			rawMessage: strings.Join([]string{
				"From: from@example.net",
				"Reply-To: reply@example.net",
				"",
				"body",
			}, "\r\n"),
			want: "env@example.net",
		},
		{
			name:         "reply-to fallback",
			envelopeFrom: "",
			rawMessage: strings.Join([]string{
				"From: from@example.net",
				"Reply-To: reply@example.net",
				"",
				"body",
			}, "\r\n"),
			want: "reply@example.net",
		},
		{
			name:         "from fallback",
			envelopeFrom: "",
			rawMessage: strings.Join([]string{
				"From: from@example.net",
				"",
				"body",
			}, "\r\n"),
			want: "from@example.net",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			header := makeHeader(tc.rawMessage)
			got, err := selectReplyRecipient(tc.envelopeFrom, header)
			if err != nil {
				t.Fatalf("selectReplyRecipient() error = %v", err)
			}
			if got != tc.want {
				t.Fatalf("selectReplyRecipient() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestReadReplyBody_PrefersPlainTextPart(t *testing.T) {
	inbound := strings.Join([]string{
		"From: sender@example.net",
		"To: echo@example.com",
		"Subject: multipart",
		"MIME-Version: 1.0",
		`Content-Type: multipart/alternative; boundary="part-boundary"`,
		"",
		"--part-boundary",
		`Content-Type: text/plain; charset="UTF-8"`,
		"",
		"Hopefully you echo this back to me!",
		"--part-boundary",
		`Content-Type: text/html; charset="UTF-8"`,
		"",
		"<div dir=\"ltr\">Hopefully you echo this back to me!</div>",
		"--part-boundary--",
		"",
	}, "\r\n")

	reader, err := mail.CreateReader(strings.NewReader(inbound))
	if err != nil {
		t.Fatalf("CreateReader() error = %v", err)
	}

	body, err := readReplyBody(reader, []byte(inbound))
	if err != nil {
		t.Fatalf("readReplyBody() error = %v", err)
	}

	if !strings.Contains(body.Plain, "Hopefully you echo this back to me!") {
		t.Fatalf("plain body missing expected text, got: %q", body.Plain)
	}
	if !strings.Contains(body.HTML, "<div dir=\"ltr\">Hopefully you echo this back to me!</div>") {
		t.Fatalf("html body missing expected markup, got: %q", body.HTML)
	}
}

func TestReadReplyBody_HTMLFallbackStripsMarkup(t *testing.T) {
	inbound := strings.Join([]string{
		"From: sender@example.net",
		"To: echo@example.com",
		"Subject: html-only",
		"MIME-Version: 1.0",
		`Content-Type: multipart/alternative; boundary="html-boundary"`,
		"",
		"--html-boundary",
		`Content-Type: text/html; charset="UTF-8"`,
		"",
		"<div dir=\"ltr\">Hello <b>there</b>&amp;friends</div>",
		"--html-boundary--",
		"",
	}, "\r\n")

	reader, err := mail.CreateReader(strings.NewReader(inbound))
	if err != nil {
		t.Fatalf("CreateReader() error = %v", err)
	}

	body, err := readReplyBody(reader, []byte(inbound))
	if err != nil {
		t.Fatalf("readReplyBody() error = %v", err)
	}

	if !strings.Contains(body.HTML, "<div dir=\"ltr\">Hello <b>there</b>&amp;friends</div>") {
		t.Fatalf("html body should preserve original markup, got: %q", body.HTML)
	}
	if body.Plain != "Hello there &friends" {
		t.Fatalf("plain body = %q, want %q", body.Plain, "Hello there &friends")
	}
}

func TestReplierEcho_MultipartReplyContainsPlainAndHTML(t *testing.T) {
	cfg := config.Config{
		Hostname: "echo.example.com",
		Reply: config.ReplyConfig{
			FromAddress: "echo@example.com",
			MailFrom:    "bounce@example.com",
		},
	}

	replier, err := NewReplier(cfg, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatalf("NewReplier() error = %v", err)
	}

	var deliveredMessage []byte
	replier.deliverFn = func(_ context.Context, _ string, message []byte) error {
		deliveredMessage = append([]byte(nil), message...)
		return nil
	}

	inbound := strings.Join([]string{
		"From: sender@example.net",
		"To: echo@example.com",
		"Subject: multipart",
		"MIME-Version: 1.0",
		`Content-Type: multipart/alternative; boundary="reply-boundary"`,
		"",
		"--reply-boundary",
		`Content-Type: text/plain; charset="UTF-8"`,
		"",
		"Plain part",
		"--reply-boundary",
		`Content-Type: text/html; charset="UTF-8"`,
		"",
		"<div><b>HTML</b> part</div>",
		"--reply-boundary--",
		"",
	}, "\r\n")

	if err := replier.Echo(context.Background(), InboundMessage{
		EnvelopeFrom: "sender@example.net",
		Data:         []byte(inbound),
	}); err != nil {
		t.Fatalf("Echo() error = %v", err)
	}

	replyText := string(deliveredMessage)
	if !strings.Contains(replyText, "Content-Type: multipart/alternative;") {
		t.Fatalf("reply should be multipart/alternative, got:\n%s", replyText)
	}
	if !strings.Contains(replyText, "Content-Type: text/plain; charset=utf-8") {
		t.Fatalf("reply missing text/plain part, got:\n%s", replyText)
	}
	if !strings.Contains(replyText, "Content-Type: text/html; charset=utf-8") {
		t.Fatalf("reply missing text/html part, got:\n%s", replyText)
	}
	if !strings.Contains(replyText, "Plain part") {
		t.Fatalf("reply missing plain payload, got:\n%s", replyText)
	}
	if !strings.Contains(replyText, "<div><b>HTML</b> part</div>") {
		t.Fatalf("reply missing html payload, got:\n%s", replyText)
	}
}

func TestReplierEcho_DKIMSignatureAddedWhenEnabled(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}

	privateKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	})

	keyPath := t.TempDir() + "/dkim-private.pem"
	if err := os.WriteFile(keyPath, privateKeyPEM, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cfg := config.Config{
		Hostname: "mailtest.example.com",
		Reply: config.ReplyConfig{
			FromAddress: "echo@mailtest.example.com",
			MailFrom:    "bounce@mailtest.example.com",
		},
		DKIM: &config.DKIMConfig{
			Domain:         "mailtest.example.com",
			Selector:       "s1",
			PrivateKeyPath: keyPath,
		},
	}

	replier, err := NewReplier(cfg, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatalf("NewReplier() error = %v", err)
	}

	var deliveredMessage []byte
	replier.deliverFn = func(_ context.Context, _ string, message []byte) error {
		deliveredMessage = append([]byte(nil), message...)
		return nil
	}

	inbound := strings.Join([]string{
		"From: sender@example.net",
		"To: echo@example.com",
		"Subject: dkim",
		"",
		"hello",
		"",
	}, "\r\n")

	if err := replier.Echo(context.Background(), InboundMessage{
		EnvelopeFrom: "sender@example.net",
		Data:         []byte(inbound),
	}); err != nil {
		t.Fatalf("Echo() error = %v", err)
	}

	if !strings.Contains(string(deliveredMessage), "DKIM-Signature:") {
		t.Fatalf("expected DKIM-Signature header, got:\n%s", string(deliveredMessage))
	}
}

func TestNewReplier_DKIMRejectsNonRSAKey(t *testing.T) {
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}

	pkcs8Bytes, err := x509.MarshalPKCS8PrivateKey(ecKey)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey() error = %v", err)
	}

	privateKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: pkcs8Bytes,
	})

	keyPath := t.TempDir() + "/dkim-private.pem"
	if err := os.WriteFile(keyPath, privateKeyPEM, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cfg := config.Config{
		Hostname: "mailtest.example.com",
		Reply: config.ReplyConfig{
			FromAddress: "echo@mailtest.example.com",
			MailFrom:    "bounce@mailtest.example.com",
		},
		DKIM: &config.DKIMConfig{
			Domain:         "mailtest.example.com",
			Selector:       "s1",
			PrivateKeyPath: keyPath,
		},
	}

	_, err = NewReplier(cfg, log.New(io.Discard, "", 0))
	if err == nil {
		t.Fatalf("NewReplier() expected error for non-RSA DKIM key")
	}
	if !strings.Contains(err.Error(), "use RSA private key") {
		t.Fatalf("NewReplier() error = %q, expected RSA guidance", err)
	}
}
