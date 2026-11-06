VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo 'main')
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo 'unknown')
BUILD_TIME ?= $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')
BUILD_DIR ?= bin
DIST_DIR ?= $(BUILD_DIR)/dist
PREFIX ?= /usr/local
BINDIR ?= $(PREFIX)/bin

# Strip debugging symbols and inject version info
LDFLAGS ?= -s -w -X 'main.version=$(VERSION)'

COMMANDS = ansible ansible-playbook ansible-inventory ansible-vault ansible-doc ansible-config ansible-galaxy ansible-pull go-ansible

# Supported cross-compilation matrix
DARWIN_ARCHS = amd64 arm64
LINUX_ARCHS = amd64 arm64
WINDOWS_ARCHS = amd64 arm64

.PHONY: all build multicall install darwin linux windows cross-build dist deps test lint fmt vet clean help

# Default target: build host-platform binaries
all: clean build

# Build single unified multicall binary with BusyBox-style symlinks
multicall:
	@echo "==> Building unified go-ansible multicall binary ($(VERSION))..."
	@mkdir -p $(BUILD_DIR)
	@CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/go-ansible ./cmd/go-ansible
	@echo "  -> Creating symlinks for all 8 Ansible tools in $(BUILD_DIR)/..."
	@for cmd in ansible ansible-playbook ansible-inventory ansible-vault ansible-doc ansible-config ansible-galaxy ansible-pull; do \
		ln -sf go-ansible $(BUILD_DIR)/$${cmd}; \
	done
	@echo "==> Multicall binary and symlinks ready in $(BUILD_DIR)/"

# Build all 8 CLI tools for the current host environment
build:
	@echo "==> Building 8 Ansible replacement binaries for host OS ($(VERSION))..."
	@mkdir -p $(BUILD_DIR)
	@for cmd in $(COMMANDS); do \
		echo "  -> Building $${cmd}..."; \
		CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$${cmd} ./cmd/$${cmd} || exit 1; \
	done
	@echo "==> Build complete! Host binaries are in $(BUILD_DIR)/"

# Directly install binaries to system PATH (default: /usr/local/bin, override with PREFIX=/usr)
install: build
	@echo "==> Installing binaries to $(BINDIR)..."
	@install -d $(BINDIR)
	@for cmd in $(COMMANDS); do \
		echo "  -> Installing $${cmd} to $(BINDIR)..."; \
		install -m 755 $(BUILD_DIR)/$${cmd} $(BINDIR)/$${cmd}; \
	done
	@echo "==> Installation complete! Python ansible CLI tools in $(BINDIR) have been replaced."

# Build for macOS (Darwin amd64 + arm64 Apple Silicon)
darwin:
	@echo "==> Building for macOS (Darwin)..."
	@for arch in $(DARWIN_ARCHS); do \
		target_dir="$(BUILD_DIR)/darwin-$${arch}"; \
		mkdir -p "$${target_dir}"; \
		echo "  -> Building darwin/$${arch} into $${target_dir}/..."; \
		for cmd in $(COMMANDS); do \
			CGO_ENABLED=0 GOOS=darwin GOARCH=$${arch} go build -ldflags "$(LDFLAGS)" -o "$${target_dir}/$${cmd}" ./cmd/$${cmd} || exit 1; \
		done; \
	done
	@echo "==> macOS builds complete in $(BUILD_DIR)/darwin-*/"

# Build for Linux (Linux amd64 + arm64)
linux:
	@echo "==> Building for Linux..."
	@for arch in $(LINUX_ARCHS); do \
		target_dir="$(BUILD_DIR)/linux-$${arch}"; \
		mkdir -p "$${target_dir}"; \
		echo "  -> Building linux/$${arch} into $${target_dir}/..."; \
		for cmd in $(COMMANDS); do \
			CGO_ENABLED=0 GOOS=linux GOARCH=$${arch} go build -ldflags "$(LDFLAGS)" -o "$${target_dir}/$${cmd}" ./cmd/$${cmd} || exit 1; \
		done; \
	done
	@echo "==> Linux builds complete in $(BUILD_DIR)/linux-*/"

# Build for Windows (Windows amd64 + arm64, .exe extension)
windows:
	@echo "==> Building for Windows..."
	@for arch in $(WINDOWS_ARCHS); do \
		target_dir="$(BUILD_DIR)/windows-$${arch}"; \
		mkdir -p "$${target_dir}"; \
		echo "  -> Building windows/$${arch} into $${target_dir}/..."; \
		for cmd in $(COMMANDS); do \
			CGO_ENABLED=0 GOOS=windows GOARCH=$${arch} go build -ldflags "$(LDFLAGS)" -o "$${target_dir}/$${cmd}.exe" ./cmd/$${cmd} || exit 1; \
		done; \
	done
	@echo "==> Windows builds complete in $(BUILD_DIR)/windows-*/"

# Build for all platforms (macOS, Linux, Windows)
cross-build: darwin linux windows
	@echo "==> All cross-platform builds completed successfully!"

# Package cross-platform distribution archives (.tar.gz and .zip)
dist: clean cross-build
	@echo "==> Packaging distribution archives into $(DIST_DIR)..."
	@mkdir -p $(DIST_DIR)
	@for arch in $(DARWIN_ARCHS); do \
		echo "  -> Packaging darwin-$${arch}..."; \
		tar -czf "$(DIST_DIR)/ansible-darwin-$${arch}.tar.gz" -C "$(BUILD_DIR)/darwin-$${arch}" . ; \
	done
	@for arch in $(LINUX_ARCHS); do \
		echo "  -> Packaging linux-$${arch}..."; \
		tar -czf "$(DIST_DIR)/ansible-linux-$${arch}.tar.gz" -C "$(BUILD_DIR)/linux-$${arch}" . ; \
	done
	@for arch in $(WINDOWS_ARCHS); do \
		echo "  -> Packaging windows-$${arch}..."; \
		(cd "$(BUILD_DIR)/windows-$${arch}" && zip -q -r "../../$(DIST_DIR)/ansible-windows-$${arch}.zip" .) ; \
	done
	@echo "==> Distribution archives packaged successfully in $(DIST_DIR)/:"
	@ls -lh $(DIST_DIR)

# Download and tidy Go dependencies
deps:
	@echo "==> Downloading dependencies..."
	@go mod download
	@go mod tidy

# Run all unit tests
test:
	@echo "==> Running tests..."
	@go test -v -count=1 ./...

# Run golangci-lint
lint:
	@echo "==> Running linter..."
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run ./...; \
	else \
		echo "golangci-lint not installed. Run: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest"; \
		exit 1; \
	fi

# Format code
fmt:
	@echo "==> Formatting code..."
	@go fmt ./...

# Run go vet
vet:
	@echo "==> Vetting code..."
	@go vet ./...

# Clean build and package artifacts
clean:
	@echo "==> Cleaning up build files..."
	@rm -rf $(BUILD_DIR)
	@rm -rf ./logs ./uploads
	@echo "Clean complete"

# Show help
help:
	@echo "Available make targets:"
	@echo "  build        - Build 8 CLI binaries for host system into bin/ (default)"
	@echo "  install      - Install host binaries to PREFIX/bin (default: /usr/local/bin)"
	@echo "  darwin       - Build for macOS (darwin-amd64 and darwin-arm64)"
	@echo "  linux        - Build for Linux (linux-amd64 and linux-arm64)"
	@echo "  windows      - Build for Windows (windows-amd64.exe and windows-arm64.exe)"
	@echo "  cross-build  - Build all platforms (macOS, Linux, Windows)"
	@echo "  dist         - Build and package tar.gz/zip archives into bin/dist/"
	@echo "  test         - Run all unit tests"
	@echo "  deps         - Download and tidy Go module dependencies"
	@echo "  lint         - Run golangci-lint"
	@echo "  fmt          - Format Go code using go fmt"
	@echo "  vet          - Run go vet"
	@echo "  clean        - Remove build and dist artifacts"
	@echo "  help         - Show this help message"
