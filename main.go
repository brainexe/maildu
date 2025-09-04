package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/joho/godotenv"
	"maildu/internal/cache"
	"maildu/internal/imap"
	"maildu/internal/ui"
)

func init() {
	// Load .env file if it exists
	if err := godotenv.Load(); err != nil {
		// Don't fail if .env file doesn't exist, just continue
		log.Printf("No .env file found or error loading it: %v", err)
	}
}

func main() {
	log.Printf("Starting imapdu application...")

	log.Printf("Loading configuration...")
	cfg, err := imap.LoadConfig()
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("Configuration loaded successfully. Server: %s, User: %s", cfg.Server, cfg.Username)

	log.Printf("Opening cache at: %s", cfg.Cache)
	cache, err := cache.Open(cfg.Cache)
	if err != nil {
		log.Fatal(err)
	}
	defer cache.Close()
	log.Printf("Cache opened successfully")

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	log.Printf("Connecting to IMAP server %s:%d...", cfg.Server, cfg.Port)
	ic, err := imap.Dial(ctx, cfg)
	if err != nil {
		log.Printf("Failed to connect to IMAP server: %v", err)
		log.Fatal(err)
	}
	defer ic.Logout()

	log.Printf("Starting TUI...")
	p := ui.NewProgram(cfg, cache, ic)
	if _, err := p.Run(); err != nil {
		log.Fatal(err)
	}
}
