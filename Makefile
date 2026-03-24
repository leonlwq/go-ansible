APP="$(notdir $(CURDIR))"
MAIN_PATH="cmd/${APP}/main.go"
VERSION="$(shell git rev-parse --abbrev-ref HEAD 2>/dev/null || echo 'unknown')"
COMMIT="$(shell git rev-parse --short HEAD 2>/dev/null || echo 'unknown')"
BUILD_TIME="$(shell date -u '+%Y-%m-%dT%H:%M:%SZ')"
BUILD_DIR="bin"
LDFLAGS="-X '${APP}/internal/version.Version=$(VERSION)' -X '${APP}/internal/version.Commit=$(COMMIT)' -X '${APP}/internal/version.BuildTime=$(BUILD_TIME)'"

.PHONY: all build clean run test lint fmt vet install darwin linux windows help

# Default target
all: clean build

# Build the application
build:
	@echo "Building $(APP) version $(VERSION)..."
	@mkdir -p $(BUILD_DIR)
	@go build -ldflags $(LDFLAGS) -o $(BUILD_DIR)/$(APP) $(MAIN_PATH)
	@echo "Build complete: $(BUILD_DIR)/$(APP)"

# Install dependencies
install:
	@echo "Installing dependencies..."
	@go mod download
	@go mod tidy

# Build for macOS
darwin:
	@echo "Building for macOS (amd64)..."
	@mkdir -p $(BUILD_DIR)
	@CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -ldflags $(LDFLAGS) -o $(BUILD_DIR)/$(APP)-darwin-amd64 $(MAIN_PATH)
	@echo "Build complete: $(BUILD_DIR)/$(APP)-darwin-amd64"

# Build for Linux
linux:
	@echo "Building for Linux (amd64)..."
	@mkdir -p $(BUILD_DIR)
	@CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags $(LDFLAGS) -o $(BUILD_DIR)/$(APP)-linux-amd64 $(MAIN_PATH)
	@echo "Build complete: $(BUILD_DIR)/$(APP)-linux-amd64"

# Build for Windows
windows:
	@echo "Building for Windows (amd64)..."
	@mkdir -p $(BUILD_DIR)
	@CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -ldflags $(LDFLAGS) -o $(BUILD_DIR)/$(APP)-windows-amd64.exe $(MAIN_PATH)
	@echo "Build complete: $(BUILD_DIR)/$(APP)-windows-amd64.exe"

all:
	@make darwin linux windows
	@echo "All builds completed"

# Build for all platforms
cross-build: darwin linux windows
	@echo "Cross-platform builds completed"

# Run the application
run:
	@echo "Running $(APP)..."
	@go run $(MAIN_PATH)

# Run tests
test:
	@echo "Running tests..."
	@go test -v ./...

# Run linter
lint:
	@echo "Running linter..."
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run ./...; \
	else \
		echo "golangci-lint not installed. Run: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest"; \
		exit 1; \
	fi

# Format code
fmt:
	@echo "Formatting code..."
	@go fmt ./...

# Run go vet
vet:
	@echo "Vetting code..."
	@go vet ./...

# Clean build artifacts
clean:
	@echo "Cleaning up build files..."
	@rm -rf $(BUILD_DIR)
	@rm -rf ./logs ./uploads
	@echo "Clean complete"

# Show help
help:
	@echo "Available targets:"
	@echo "  all          - Clean and build the application (default)"
	@echo "  build        - Build the application"
	@echo "  install      - Install dependencies"
	@echo "  darwin       - Build for macOS"
	@echo "  linux        - Build for Linux"
	@echo "  windows      - Build for Windows"
	@echo "  cross-build  - Build for all platforms"
	@echo "  run          - Run the application"
	@echo "  test         - Run tests"
	@echo "  lint         - Run linter"
	@echo "  fmt          - Format code"
	@echo "  vet          - Run go vet"
	@echo "  clean        - Clean build artifacts"
	@echo "  help         - Show this help message"
