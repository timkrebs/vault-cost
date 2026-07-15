BINARY   := vault.ocplugin
IMAGE    ?= vault-ocplugin:dev
GOARCH   ?= amd64
LDFLAGS  := -s -w

.PHONY: all check tidy fmt lint test cover vulncheck build build-all image clean

all: tidy lint test build

# Run the same gates CI enforces.
check: fmt lint test vulncheck

tidy:
	go mod tidy

fmt:
	gofmt -w .

lint:
	golangci-lint run

test:
	go test ./... -race -count=1

cover:
	go test ./... -covermode=atomic -coverprofile=coverage.out
	go tool cover -func=coverage.out | tail -n1

vulncheck:
	go run golang.org/x/vuln/cmd/govulncheck@latest ./...

# Build a single linux binary named per the OpenCost convention:
#   vault.ocplugin.linux.<arch>
build:
	CGO_ENABLED=0 GOOS=linux GOARCH=$(GOARCH) \
		go build -trimpath -ldflags="$(LDFLAGS)" \
		-o bin/$(BINARY).linux.$(GOARCH) ./cmd/vault

build-all:
	$(MAKE) build GOARCH=amd64
	$(MAKE) build GOARCH=arm64

# Multi-arch init-container image (requires docker buildx). Push with --push.
image:
	docker buildx build --platform=linux/amd64,linux/arm64 -t $(IMAGE) --load .

clean:
	rm -rf bin coverage.out
