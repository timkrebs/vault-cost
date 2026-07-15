# syntax=docker/dockerfile:1
#
# Multi-arch init-container image for the Vault OpenCost plugin.
# It carries the plugin binary + a default config and, on start, copies them
# into the plugin directory shared with the Kubecost cost-model container.
#
# Build (run `make tidy` first so go.sum exists):
#   docker buildx build --platform=linux/amd64,linux/arm64 -t <registry>/vault-ocplugin:<tag> --push .

FROM --platform=$BUILDPLATFORM golang:1.24 AS build
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" \
    -o /out/vault.ocplugin.linux.${TARGETARCH} ./cmd/vault

FROM alpine:3.20
ARG TARGETARCH
RUN adduser -D -u 65532 nonroot
# Artifacts staged here; ship-plugin.sh copies them into the shared volume.
COPY --from=build /out/vault.ocplugin.linux.${TARGETARCH} /artifacts/bin/vault.ocplugin.linux.${TARGETARCH}
COPY deploy/plugin-config/vault.json /artifacts/config/vault.json
COPY deploy/ship-plugin.sh /usr/local/bin/ship-plugin.sh
RUN chmod +x /usr/local/bin/ship-plugin.sh /artifacts/bin/*
USER nonroot
ENTRYPOINT ["/usr/local/bin/ship-plugin.sh"]
