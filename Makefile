# Makefile for maildu

# Variables
BINARY_NAME=maildu
MAIN_DIR=cmd/imapdu

# Default target
.PHONY: all
all: build

# Build the binary
.PHONY: build
build:
	go build -o $(BINARY_NAME) ./$(MAIN_DIR)

# Run the application
.PHONY: run
run:
	go run ./$(MAIN_DIR)

# Clean build artifacts
.PHONY: clean
clean:
	rm -f $(BINARY_NAME)

# Install dependencies
.PHONY: deps
deps:
	go mod download
	go mod tidy

# Run tests
.PHONY: test
test:
	go test ./...

# Show help
.PHONY: help
help:
	@echo "Available targets:"
	@echo "  build  - Build the binary"
	@echo "  run    - Run the application"
	@echo "  clean  - Remove build artifacts"
	@echo "  deps   - Download and tidy dependencies"
	@echo "  test   - Run tests"
	@echo "  help   - Show this help message"
