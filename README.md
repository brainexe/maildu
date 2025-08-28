# Maildu (IMAP Disk Usage Analyzer)

A terminal-based IMAP email client that helps you analyze disk usage across your mailboxes. Built with Go and featuring a beautiful TUI interface powered by Bubble Tea.

## Features

- 📊 Analyze disk usage across IMAP mailboxes
- 🔍 Interactive terminal user interface
- ⚡ Parallel message fetching for better performance
- 💾 Smart caching to avoid redundant IMAP requests
- 🔐 TLS support with optional certificate verification skip
- 📱 Support for various IMAP providers (Gmail, Outlook, etc.)

## Installation

### From Source

```bash
git clone <repository-url>
cd maildu
go build -o maildu mail.go
```

### Dependencies

This project uses Go modules. Dependencies will be automatically downloaded when building.

## Configuration

### Environment Variables

Copy `.env.example` to `.env` and configure your IMAP settings:

```bash
cp .env.example .env
```

Edit `.env` with your IMAP server details:

| Variable | Description | Default | Required |
|----------|-------------|---------|----------|
| `IMAP_SERVER` | IMAP server hostname | - | ✅ |
| `IMAP_PORT` | IMAP server port | 993 | ❌ |
| `IMAP_TLS` | Use TLS encryption | true | ❌ |
| `IMAP_SKIP_CERT_VERIFY` | Skip TLS certificate verification | false | ❌ |
| `IMAP_USER` | IMAP username/email | - | ✅ |
| `IMAP_PASS` | IMAP password | - | ✅ |
| `IMAPDU_CACHE` | Cache file path | ~/.local/share/imapdu/imapdu-cache.bolt | ❌ |
| `IMAP_TIMEOUT` | Network timeout | 30s | ❌ |

### Command Line Flags

Alternatively, you can use command line flags:

```bash
./maildu -server imap.gmail.com -user your-email@gmail.com -pass your-password
```

Available flags:
- `-server`: IMAP server host
- `-port`: IMAP server port
- `-tls`: Use TLS (true/false)
- `-skip-cert-verify`: Skip TLS certificate verification
- `-user`: IMAP username
- `-pass`: IMAP password
- `-cache`: Cache file path
- `-timeout`: Network timeout

## Usage

1. Configure your IMAP settings in `.env` or via command line flags
2. Run the application:
   ```bash
   ./maildu
   ```
3. Navigate through your mailboxes using the interactive interface

## Common IMAP Providers

### Gmail
```env
IMAP_SERVER=imap.gmail.com
IMAP_PORT=993
IMAP_TLS=true
```
*Note: You may need to use an app-specific password instead of your regular Gmail password.*

### Outlook/Hotmail
```env
IMAP_SERVER=outlook.office365.com
IMAP_PORT=993
IMAP_TLS=true
```

### Yahoo Mail
```env
IMAP_SERVER=imap.mail.yahoo.com
IMAP_PORT=993
IMAP_TLS=true
```

## Security Notes

- Store sensitive credentials in `.env` file and ensure it's not committed to version control
- Add `.env` to your `.gitignore` file
- Consider using app-specific passwords when available
- For testing only: use `IMAP_SKIP_CERT_VERIFY=true` for self-signed certificates

## Caching

The application uses a local BoltDB cache to store message metadata and avoid redundant IMAP requests. The cache is stored at:
- Default: `~/.local/share/imapdu/imapdu-cache.bolt`
- Custom: Set via `IMAPDU_CACHE` environment variable

## Performance

- Parallel fetching: The app fetches message sizes in parallel (8 workers by default)
- Refresh interval: 3 minutes between IDLE refreshes
- Smart caching: Only fetches new or changed messages

## Troubleshooting

### Connection Issues
- Verify your IMAP server settings
- Check if your email provider requires app-specific passwords
- Try increasing the timeout value
- For testing, temporarily set `IMAP_SKIP_CERT_VERIFY=true`

### Authentication Errors
- Ensure IMAP is enabled in your email account settings
- Use app-specific passwords for Gmail and other providers
- Check username format (some providers require full email address)

## License

[Add your license information here]

## Contributing

[Add contribution guidelines here]
