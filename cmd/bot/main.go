// Joe's Honeypot — Discord honeypot bot.
package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/bkan0n/joeshoneypot/internal/bot"
	"github.com/bkan0n/joeshoneypot/internal/store"
)

func main() {
	level := slog.LevelInfo
	if os.Getenv("LOG_LEVEL") == "debug" {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level}))

	token := os.Getenv("BOT_TOKEN")
	if token == "" {
		log.Error("BOT_TOKEN is required")
		os.Exit(1)
	}
	dbPath := os.Getenv("DB_PATH")
	if dbPath == "" {
		dbPath = "/data/honeypot.db"
	}

	st, err := store.Open(dbPath)
	if err != nil {
		log.Error("opening database", "path", dbPath, "err", err)
		os.Exit(1)
	}
	defer func() {
		if err := st.Close(); err != nil {
			log.Error("closing database", "err", err)
		}
	}()

	b, err := bot.New(token, st, log)
	if err != nil {
		log.Error("creating bot", "err", err)
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	if err := b.Start(ctx); err != nil {
		log.Error("starting bot", "err", err)
		os.Exit(1)
	}
	log.Info("Joe's Honeypot is running", "db", dbPath)
	<-ctx.Done()
	log.Info("shutting down")
	b.Close(context.Background())
}
