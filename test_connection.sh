#!/bin/bash

source .env

echo "Testing IMAP connection..."
echo "Environment variables:"
echo "IMAP_SERVER: ${IMAP_SERVER:-not set}"
echo "IMAP_PORT: ${IMAP_PORT:-not set}"
echo "IMAP_USER: ${IMAP_USER:-not set}"
echo "IMAP_PASS: ${IMAP_PASS:-not set}"
echo "IMAP_TIMEOUT: ${IMAP_TIMEOUT:-not set}"

echo ""
echo "Testing network connectivity..."
if [ -n "$IMAP_SERVER" ] && [ -n "$IMAP_PORT" ]; then
    echo "Testing connection to $IMAP_SERVER:$IMAP_PORT..."
    timeout 10 nc -zv "$IMAP_SERVER" "$IMAP_PORT" 2>&1 || echo "Connection test failed"
else
    echo "IMAP_SERVER or IMAP_PORT not set, skipping connection test"
fi

echo ""
echo "To run with custom timeout:"
echo "IMAP_TIMEOUT=5m make run"
echo ""
echo "To run with debug logging:"
echo "go run -v ./cmd/imapdu"
