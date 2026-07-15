BINARY   := vault.ocplugin
IMAGE    ?= vault-ocplugin:dev
GOARCH   ?= amd64
LDFLAGS  := -s -w

.PHONY: all tidy test cover build build-all image clean

all: tidy test build

tidy:
	go mod tidy

test:
	go test ./... -count=1

cover:
	go test ./... -covermode=count -coverprofile=coverage.out
	go tool cover -func=coverage.out | tail -n1

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
