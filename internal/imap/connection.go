package imap

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/emersion/go-imap/client"
)

type Conn struct {
	cl     *client.Client
	cfg    Config
	mu     sync.Mutex
	authed bool
}

func Dial(ctx context.Context, cfg Config) (*Conn, error) {
	dialer := &net.Dialer{Timeout: cfg.Timeout}
	var c *client.Client
	var err error

	// Try to connect with retries
	maxRetries := 3
	for attempt := 0; attempt < maxRetries; attempt++ {
		if cfg.TLS {
			tlsCfg := &tls.Config{
				ServerName:         cfg.Server,
				InsecureSkipVerify: cfg.SkipCert, // if true, user accepts risk
			}
			c, err = client.DialWithDialerTLS(dialer, fmt.Sprintf("%s:%d", cfg.Server, cfg.Port), tlsCfg)
		} else {
			c, err = client.DialWithDialer(dialer, fmt.Sprintf("%s:%d", cfg.Server, cfg.Port))
		}
		if err == nil {
			break
		}
		if attempt < maxRetries-1 {
			log.Printf("Connection attempt %d failed: %v, retrying...", attempt+1, err)
			time.Sleep(time.Duration(attempt+1) * time.Second)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("failed to connect after %d attempts: %w", maxRetries, err)
	}

	// Login
	if err = c.Login(cfg.Username, cfg.Password); err != nil {
		_ = c.Logout()
		return nil, fmt.Errorf("failed to login: %w", err)
	}
	return &Conn{cl: c, cfg: cfg, authed: true}, nil
}

func (ic *Conn) Logout() error {
	ic.mu.Lock()
	defer ic.mu.Unlock()
	if ic.cl != nil {
		return ic.cl.Logout()
	}
	return nil
}

// ensureAuthenticated checks if the connection is still authenticated and reconnects if needed
func (ic *Conn) ensureAuthenticated(ctx context.Context) error {
	ic.mu.Lock()
	defer ic.mu.Unlock()

	// Check if we have a valid connection by trying a simple operation
	if ic.cl != nil && ic.authed {
		// Try to get the server capability to test if connection is still valid
		_, err := ic.cl.Capability()
		if err == nil {
			return nil // Connection is still good
		}

		// Check for specific error types
		if strings.Contains(err.Error(), "short write") ||
			strings.Contains(err.Error(), "connection reset") ||
			strings.Contains(err.Error(), "broken pipe") {
			log.Printf("IMAP connection lost due to network issue (%v), attempting to reconnect...", err)
		} else {
			log.Printf("IMAP connection lost, attempting to reconnect: %v", err)
		}

		// Connection is bad, clean up
		ic.cl.Logout()
		ic.cl = nil
		ic.authed = false
	}

	// Reconnect with retries
	maxRetries := 3
	for attempt := 0; attempt < maxRetries; attempt++ {
		dialer := &net.Dialer{Timeout: ic.cfg.Timeout}
		var c *client.Client
		var err error

		if ic.cfg.TLS {
			tlsCfg := &tls.Config{
				ServerName:         ic.cfg.Server,
				InsecureSkipVerify: ic.cfg.SkipCert,
			}
			c, err = client.DialWithDialerTLS(dialer, fmt.Sprintf("%s:%d", ic.cfg.Server, ic.cfg.Port), tlsCfg)
		} else {
			c, err = client.DialWithDialer(dialer, fmt.Sprintf("%s:%d", ic.cfg.Server, ic.cfg.Port))
		}

		if err != nil {
			if attempt < maxRetries-1 {
				log.Printf("Reconnection attempt %d failed: %v, retrying...", attempt+1, err)
				time.Sleep(time.Duration(attempt+1) * time.Second)
				continue
			}
			return fmt.Errorf("failed to dial IMAP server after %d attempts: %w", maxRetries, err)
		}

		// Login
		if err = c.Login(ic.cfg.Username, ic.cfg.Password); err != nil {
			c.Logout()
			if attempt < maxRetries-1 {
				log.Printf("Reconnection login attempt %d failed: %v, retrying...", attempt+1, err)
				time.Sleep(time.Duration(attempt+1) * time.Second)
				continue
			}
			return fmt.Errorf("failed to login to IMAP server after %d attempts: %w", maxRetries, err)
		}

		ic.cl = c
		ic.authed = true
		log.Printf("Successfully reconnected to IMAP server")
		return nil
	}

	return fmt.Errorf("failed to reconnect after %d attempts", maxRetries)
}

// GetClient returns the underlying IMAP client
func (ic *Conn) GetClient() *client.Client {
	ic.mu.Lock()
	defer ic.mu.Unlock()
	return ic.cl
}
