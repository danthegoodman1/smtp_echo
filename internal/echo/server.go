package echo

import (
	"context"
	"fmt"
	"io"
	"log"

	"github.com/emersion/go-smtp"
)

type InboundMessage struct {
	EnvelopeFrom string
	Recipients   []string
	Data         []byte
}

type Processor interface {
	Echo(ctx context.Context, msg InboundMessage) error
}

type Backend struct {
	processor Processor
	logger    *log.Logger
}

func NewBackend(processor Processor, logger *log.Logger) *Backend {
	return &Backend{
		processor: processor,
		logger:    logger,
	}
}

func (b *Backend) NewSession(_ *smtp.Conn) (smtp.Session, error) {
	return &session{
		backend: b,
	}, nil
}

type session struct {
	backend      *Backend
	envelopeFrom string
	recipients   []string
}

func (s *session) Reset() {
	s.envelopeFrom = ""
	s.recipients = s.recipients[:0]
}

func (s *session) Logout() error {
	return nil
}

func (s *session) Mail(from string, _ *smtp.MailOptions) error {
	s.envelopeFrom = from
	s.recipients = s.recipients[:0]
	return nil
}

func (s *session) Rcpt(to string, _ *smtp.RcptOptions) error {
	s.recipients = append(s.recipients, to)
	return nil
}

func (s *session) Data(r io.Reader) error {
	if len(s.recipients) == 0 {
		return fmt.Errorf("at least one recipient is required")
	}

	data, err := io.ReadAll(r)
	if err != nil {
		return fmt.Errorf("read message data: %w", err)
	}

	msg := InboundMessage{
		EnvelopeFrom: s.envelopeFrom,
		Recipients:   append([]string(nil), s.recipients...),
		Data:         data,
	}

	if err := s.backend.processor.Echo(context.Background(), msg); err != nil {
		return fmt.Errorf("process echo reply: %w", err)
	}

	if s.backend.logger != nil {
		s.backend.logger.Printf("echoed message from=%q recipients=%d bytes=%d", s.envelopeFrom, len(s.recipients), len(data))
	}

	return nil
}
