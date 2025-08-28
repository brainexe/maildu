package imap

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const appName = "imapdu"

type Config struct {
	Server   string
	Port     int
	TLS      bool
	Username string
	Password string
	Cache    string
	Timeout  time.Duration
	SkipCert bool
	DryRun   bool
}

func LoadConfig() (Config, error) {
	var cfg Config
	flag.StringVar(&cfg.Server, "server", os.Getenv("IMAP_SERVER"), "IMAP server host (env IMAP_SERVER)")
	flag.IntVar(&cfg.Port, "port", envInt("IMAP_PORT", 993), "IMAP server port (env IMAP_PORT)")
	flag.BoolVar(&cfg.TLS, "tls", envBool("IMAP_TLS", true), "Use TLS")
	flag.BoolVar(&cfg.SkipCert, "skip-cert-verify", envBool("IMAP_SKIP_CERT_VERIFY", false), "Skip TLS certificate verification")
	flag.StringVar(&cfg.Username, "user", os.Getenv("IMAP_USER"), "IMAP username (env IMAP_USER)")
	flag.StringVar(&cfg.Password, "pass", os.Getenv("IMAP_PASS"), "IMAP password (env IMAP_PASS)")
	flag.StringVar(&cfg.Cache, "cache", envStr("IMAPDU_CACHE", filepath.Join(defaultDataDir(), "imapdu-cache.bolt")), "Cache file path")
	flag.DurationVar(&cfg.Timeout, "timeout", envDuration("IMAP_TIMEOUT", 30*time.Second), "Network timeout")
	flag.BoolVar(&cfg.DryRun, "dry-run", envBool("IMAPDU_DRY_RUN", true), "Dry run mode - prevent actual deletion of mails/attachments (env IMAPDU_DRY_RUN)")
	flag.Parse()

	if cfg.Server == "" || cfg.Username == "" || cfg.Password == "" {
		return cfg, fmt.Errorf("missing server/user/pass; set flags or env IMAP_SERVER/IMAP_USER/IMAP_PASS")
	}
	return cfg, nil
}

func envInt(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		var i int
		fmt.Sscanf(v, "%d", &i)
		if i != 0 {
			return i
		}
	}
	return def
}

func envBool(k string, def bool) bool {
	if v := os.Getenv(k); v != "" {
		switch strings.ToLower(v) {
		case "1", "true", "yes", "y":
			return true
		case "0", "false", "no", "n":
			return false
		}
	}
	return def
}

func envStr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func envDuration(k string, def time.Duration) time.Duration {
	if v := os.Getenv(k); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}

func defaultDataDir() string {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, appName)
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", appName)
}

