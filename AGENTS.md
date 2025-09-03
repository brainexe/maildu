# AGENTS.md

This file provides guidance to AI tools when working with code in this repository.

## Project Overview

`maildu` is a terminal-based IMAP disk usage analyzer and mail management tool built in Go. It provides an interactive TUI for analyzing mailbox sizes, browsing messages, and managing emails with features like deletion and attachment stripping.

## Architecture

The project follows Go's standard project layout with the following key components:

### Core Packages

- **`cmd/imapdu/main.go`**: Application entry point that:
  - Loads configuration from flags/environment variables
  - Initializes logging to `imapdu.log`
  - Opens BoltDB cache
  - Establishes IMAP connection
  - Launches the TUI

- **`internal/imap/`**: IMAP connection and operations
  - `config.go`: Configuration management with env var and flag support
  - `connection.go`: IMAP connection handling
  - `operations.go`: Mailbox and message operations (fetch, delete, strip attachments)

- **`internal/models/`**: Core data structures
  - `MailboxInfo`: Hierarchical mailbox structure with size information
  - `MsgEntry`: Message metadata (UID, subject, from, date, size)
  - `ListItem`: UI adapter for list display
  - Utility functions for formatting (HumanBytes, Truncate)

- **`internal/ui/`**: Bubbletea TUI implementation
  - `program.go`: Main TUI logic with navigation, sorting, and actions
  - `keymap.go`: Keyboard bindings
  - Supports hierarchical mailbox navigation and message management

- **`internal/cache/`**: BoltDB-based persistent caching for message sizes and metadata

### Key Features

- **Hierarchical Navigation**: Navigate through nested mailboxes with breadcrumb path tracking
- **Dual Sorting**: Sort by size or name in both mailbox and message views  
- **Safe Operations**: Dry-run mode enabled by default to prevent accidental deletions
- **Persistent Caching**: Uses BoltDB to cache message sizes for faster subsequent runs
- **Interactive Actions**: Delete messages and strip attachments with keyboard shortcuts

## Development Commands

```bash
# Build the application
make build

# Run the application
make run

# Run with extended timeout for debugging
make run-timeout

# Install/update dependencies
make deps

# Run tests
make test

# Test connection (uses external script)
make test-conn

# Clean build artifacts
make clean
```

## Configuration

The application supports both environment variables and command-line flags:

- **IMAP Connection**: `IMAP_SERVER`, `IMAP_PORT`, `IMAP_USER`, `IMAP_PASS`
- **Security**: `IMAP_TLS`, `IMAP_SKIP_CERT_VERIFY`, `IMAP_TIMEOUT`
- **Application**: `IMAPDU_CACHE`, `IMAPDU_DRY_RUN`

Configuration is loaded in `internal/imap/config.go` with sensible defaults (TLS enabled, dry-run mode on, cache in XDG data directory).

## Key Navigation Patterns

The TUI uses a hierarchical navigation model:
- Root level shows top-level mailboxes
- Entering a mailbox with children navigates to subdirectory view
- Entering a leaf mailbox shows messages
- Back navigation (h/⌫) moves up the hierarchy
- Path tracking maintains current location context

## Development Notes

- Uses Charm's Bubbletea framework for TUI
- All external operations (IMAP, file I/O) happen in background commands
- Error handling preserves user experience with graceful fallbacks
- Logging is written to `imapdu.log` for debugging
- Message operations support both dry-run and actual execution modes
