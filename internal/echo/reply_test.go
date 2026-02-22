package echo

import (
	"bytes"
	"context"
	"io"
	"log"
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

	replier := NewReplier(cfg, log.New(io.Discard, "", 0))

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

	err := replier.Echo(context.Background(), InboundMessage{
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
	if !strings.Contains(body, "Body line 1") || !strings.Contains(body, "Body line 2") {
		t.Fatalf("reply body does not include expected content, got: %q", body)
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
