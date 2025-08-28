# maildu

A terminal-based IMAP disk usage analyzer and mail management tool.

## Project Structure

The project has been restructured into a proper Go project layout:

```
maildu/
├── cmd/imapdu/          # Main application entry point
├── internal/            # Internal packages
│   ├── cache/          # Cache management (BoltDB)
│   ├── imap/           # IMAP connection and operations
│   ├── models/         # Data structures and types
│   └── ui/             # Terminal UI components
├── go.mod              # Go module file
├── go.sum              # Go module checksums
├── Makefile            # Build and development commands
└── README.md           # This file
```

## Features

- **Mailbox Analysis**: View disk usage by mailbox
- **Message Management**: Browse, delete, and strip attachments from emails
- **Caching**: Persistent cache for message sizes and metadata
- **Terminal UI**: Interactive terminal interface with keyboard navigation
- **Dry Run Mode**: Safe testing without actual modifications

## Building

```bash
# Build the binary
make build

# Or manually
go build -o maildu ./cmd/imapdu
```

## Running

```bash
# Run the application
make run

# Or manually
go run ./cmd/imapdu

# With custom configuration
go run ./cmd/imapdu -server=imap.example.com -user=username -pass=password
```

## Configuration

Set environment variables or use command-line flags:

- `IMAP_SERVER` / `-server`: IMAP server hostname
- `IMAP_PORT` / `-port`: IMAP server port (default: 993)
- `IMAP_USER` / `-user`: IMAP username
- `IMAP_PASS` / `-pass`: IMAP password
- `IMAP_TLS` / `-tls`: Use TLS (default: true)
- `IMAPDU_DRY_RUN` / `-dry-run`: Dry run mode (default: true)

## Key Bindings

- **Navigation**: `↑/k` (up), `↓/j` (down), `enter/l` (open), `h/⌫` (back)
- **Actions**: `x` (delete), `s` (strip attachments), `r` (refresh), `t` (toggle sort)
- **Quit**: `q` or `Ctrl+C`

## Development

```bash
# Install dependencies
make deps

# Run tests
make test

# Clean build artifacts
make clean
```
