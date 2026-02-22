package config

import (
	"errors"
	"fmt"
	"net/mail"
	"os"
	"time"

	"github.com/goccy/go-yaml"
)

type Config struct {
	ListenAddr      string        `yaml:"listen_addr"`
	Hostname        string        `yaml:"hostname"`
	ReadTimeout     time.Duration `yaml:"read_timeout"`
	WriteTimeout    time.Duration `yaml:"write_timeout"`
	MaxMessageBytes int64         `yaml:"max_message_bytes"`
	Reply           ReplyConfig   `yaml:"reply"`
}

type ReplyConfig struct {
	FromAddress string `yaml:"from_address"`
	MailFrom    string `yaml:"mail_from"`
	FromName    string `yaml:"from_name"`
}

func Load(path string) (Config, error) {
	cfg := Config{
		ListenAddr:      ":25",
		ReadTimeout:     30 * time.Second,
		WriteTimeout:    30 * time.Second,
		MaxMessageBytes: 10 * 1024 * 1024,
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config yaml: %w", err)
	}

	if err := cfg.validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func (c Config) validate() error {
	if c.ListenAddr == "" {
		return errors.New("listen_addr is required")
	}
	if c.Hostname == "" {
		return errors.New("hostname is required")
	}
	if c.ReadTimeout <= 0 {
		return errors.New("read_timeout must be > 0")
	}
	if c.WriteTimeout <= 0 {
		return errors.New("write_timeout must be > 0")
	}
	if c.MaxMessageBytes <= 0 {
		return errors.New("max_message_bytes must be > 0")
	}
	if c.Reply.FromAddress == "" {
		return errors.New("reply.from_address is required")
	}
	if c.Reply.MailFrom == "" {
		return errors.New("reply.mail_from is required")
	}

	if _, err := mail.ParseAddress(c.Reply.FromAddress); err != nil {
		return fmt.Errorf("reply.from_address invalid: %w", err)
	}
	if _, err := mail.ParseAddress(c.Reply.MailFrom); err != nil {
		return fmt.Errorf("reply.mail_from invalid: %w", err)
	}

	return nil
}
