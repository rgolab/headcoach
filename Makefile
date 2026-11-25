# Build, test and package hc.

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/rouxlabs/headcoach/internal/version.Version=$(VERSION)
TARGETS := darwin/arm64 darwin/amd64 linux/arm64 linux/amd64

.PHONY: build test check install dist

build:
	go build -ldflags "$(LDFLAGS)" -o hc ./cmd/hc

test:
	go test ./...

# What must pass before a commit.
check:
	test -z "$$(gofmt -l .)"
	go vet ./...
	go test ./...

# Puts hc in GOBIN, or in GOPATH/bin.
install:
	go install -ldflags "$(LDFLAGS)" ./cmd/hc

# One static binary per target, in dist/, and their SHA-256 sums.
dist:
	@rm -rf dist
	@for target in $(TARGETS); do \
		os=$${target%/*}; arch=$${target#*/}; \
		echo "dist/hc-$$os-$$arch"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" -o dist/hc-$$os-$$arch ./cmd/hc || exit 1; \
	done
	cd dist && shasum -a 256 hc-* > SHA256SUMS
