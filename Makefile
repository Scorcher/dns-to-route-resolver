.PHONY: build test clean release snapshot lint

# Build variables
BINARY_NAME=dns-to-route-resolver
VERSION=$(shell git describe --tags --always --dirty)
COMMIT=$(shell git rev-parse HEAD)
DATE=$(shell date +"%Y-%m-%dT%H:%M:%S%z")
LDFLAGS=-ldflags "-X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)"

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get

all: test build

build:
	$(GOBUILD) -o bin/$(BINARY_NAME) $(LDFLAGS) ./cmd/$(BINARY_NAME)

test:
	$(GOTEST) -v ./...

clean:
	$(GOCLEAN)
	rm -f bin/$(BINARY_NAME)
	rm -rf dist/

# Build for all target platforms
build-all: clean
	@echo "Building for all platforms..."
	@mkdir -p dist
	GOOS=linux GOARCH=amd64 $(GOBUILD) -o dist/$(BINARY_NAME)-linux-amd64 $(LDFLAGS) ./cmd/$(BINARY_NAME)
	GOOS=linux GOARCH=arm64 $(GOBUILD) -o dist/$(BINARY_NAME)-linux-arm64 $(LDFLAGS) ./cmd/$(BINARY_NAME)
	GOOS=darwin GOARCH=amd64 $(GOBUILD) -o dist/$(BINARY_NAME)-darwin-amd64 $(LDFLAGS) ./cmd/$(BINARY_NAME)
	GOOS=darwin GOARCH=arm64 $(GOBUILD) -o dist/$(BINARY_NAME)-darwin-arm64 $(LDFLAGS) ./cmd/$(BINARY_NAME)
	GOOS=windows GOARCH=amd64 $(GOBUILD) -o dist/$(BINARY_NAME)-windows-amd64.exe $(LDFLAGS) ./cmd/$(BINARY_NAME)
	@echo "Build complete. Binaries are in the dist/ directory."

# Install dependencies
deps:
	$(GOGET) -u ./...

# Run lint
lint:
	golangci-lint run

# Build for current platform
build-local: deps
	$(GOBUILD) -o bin/$(BINARY_NAME) $(LDFLAGS) ./cmd/$(BINARY_NAME)

# Run goreleaser in snapshot mode
snapshot:
	docker run --rm --privileged \
	  -v $(PWD):/go/src/github.com/yourusername/$(BINARY_NAME) \
	  -w /go/src/github.com/yourusername/$(BINARY_NAME) \
	  -v /var/run/docker.sock:/var/run/docker.sock \
	  -e GITHUB_TOKEN=$(GITHUB_TOKEN) \
	  goreleaser/goreleaser:latest release --snapshot --clean

# Run goreleaser in release mode (for local testing)
release:
	if [ -z "$(GITHUB_TOKEN)" ]; then \
		echo "GITHUB_TOKEN is not set"; \
		exit 1; \
	fi
	docker run --rm --privileged \
	  -v $(PWD):/go/src/github.com/yourusername/$(BINARY_NAME) \
	  -w /go/src/github.com/yourusername/$(BINARY_NAME) \
	  -v /var/run/docker.sock:/var/run/docker.sock \
	  -e GITHUB_TOKEN=$(GITHUB_TOKEN) \
	  goreleaser/goreleaser:latest release --clean

# Install the binary
install: build
	sudo cp bin/$(BINARY_NAME) /usr/local/bin/$(BINARY_NAME)

# Uninstall the binary
uninstall:
	sudo rm -f /usr/local/bin/$(BINARY_NAME)

# Show help
help:
	@echo 'Available targets:'
	@echo '  build       - Build the binary for current platform'
	@echo '  build-all   - Build the binaries for any platforms'
	@echo '  test        - Run tests'
	@echo '  clean       - Remove build artifacts'
	@echo '  deps        - Install dependencies'
	@echo '  lint        - Run linter'
	@echo '  snapshot    - Build snapshot release'
	@echo '  release     - Create a release (requires GITHUB_TOKEN)'
	@echo '  install     - Install the binary'
	@echo '  uninstall   - Uninstall the binary'