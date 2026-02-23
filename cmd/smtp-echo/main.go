package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/emersion/go-smtp"

	"github.com/danthegoodman1/smtp_echo/internal/config"
	"github.com/danthegoodman1/smtp_echo/internal/echo"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	configPath := flag.String("config", "config.yaml", "Path to config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}

	logger := log.New(os.Stdout, "", log.LstdFlags|log.LUTC)
	replier, err := echo.NewReplier(cfg, logger)
	if err != nil {
		return err
	}
	backend := echo.NewBackend(replier, logger)

	server := smtp.NewServer(backend)
	server.Addr = cfg.ListenAddr
	server.Domain = cfg.Hostname
	server.ReadTimeout = cfg.ReadTimeout
	server.WriteTimeout = cfg.WriteTimeout
	server.MaxMessageBytes = cfg.MaxMessageBytes
	server.ErrorLog = logger

	logger.Printf("starting smtp echo server on %s", cfg.ListenAddr)

	serverErr := make(chan error, 1)
	go func() {
		serverErr <- server.ListenAndServe()
	}()

	shutdownSignal, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	select {
	case err := <-serverErr:
		if errors.Is(err, smtp.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("smtp server exited: %w", err)
	case <-shutdownSignal.Done():
	}

	logger.Println("shutdown signal received")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil && !errors.Is(err, smtp.ErrServerClosed) {
		return fmt.Errorf("shutdown smtp server: %w", err)
	}

	return nil
}
