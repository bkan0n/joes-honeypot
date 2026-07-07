// Joe's Honeypot — Discord honeypot bot.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bkan0n/joeshoneypot/internal/bot"
	"github.com/bkan0n/joeshoneypot/internal/store"
)

func main() {
	level := slog.LevelInfo
	if os.Getenv("LOG_LEVEL") == "debug" {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	// Anything logging through slog's default (libraries included) joins the
	// same LOG_LEVEL-controlled stream.
	slog.SetDefault(log)

	if err := run(log); err != nil {
		log.Error("bot exited", "err", err)
		os.Exit(1)
	}
}

// run holds the bot's whole lifecycle so main exits exactly once, after every
// deferred cleanup (notably the store close) has run.
func run(log *slog.Logger) error {
	token := os.Getenv("BOT_TOKEN")
	if token == "" {
		return errors.New("BOT_TOKEN is required")
	}
	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "/data/honeypot.db"
	}

	st, err := store.Open(dbPath)
	if err != nil {
		return fmt.Errorf("opening database %s: %w", dbPath, err)
	}
	defer func() {
		if err := st.Close(); err != nil {
			log.Error("closing database", "err", err)
		}
	}()

	b, err := bot.New(token, st, log)
	if err != nil {
		return fmt.Errorf("creating bot: %w", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := b.Start(ctx); err != nil {
		return fmt.Errorf("starting bot: %w", err)
	}
	log.Info("Joe's Honeypot is running", "db", dbPath)
	<-ctx.Done()
	log.Info("shutting down")
	// The signal context is already cancelled; shutdown gets its own bounded
	// one so a hung Discord call can't stall exit indefinitely.
	closeCtx, closeCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer closeCancel()
	b.Close(closeCtx)
	return nil
}
