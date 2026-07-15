#!/bin/sh
# Init-container entrypoint: stage the Vault plugin binary + default config into
# the plugin directory shared (emptyDir) with the Kubecost cost-model container.
# The upstream plugin installer only fetches official plugins, so custom plugins
# must be delivered this way.
set -eu

BIN_DIR=/opt/opencost/plugin/bin
CFG_DIR=/opt/opencost/plugin/config

mkdir -p "$BIN_DIR" "$CFG_DIR"

cp -f /artifacts/bin/* "$BIN_DIR/"
chmod +x "$BIN_DIR"/*

# Only seed config if the host has not already provided one. Kubecost's
# configs.vault (Helm) wins if present.
for f in /artifacts/config/*; do
  base=$(basename "$f")
  if [ ! -f "$CFG_DIR/$base" ]; then
    cp "$f" "$CFG_DIR/$base"
  fi
done

echo "shipped vault plugin:"
ls -la "$BIN_DIR" "$CFG_DIR"
