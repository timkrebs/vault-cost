#!/usr/bin/env bash
# vault-setup.sh - configure Vault so the Kubecost "vault" cost plugin can
# authenticate (Kubernetes auth) and read the client Activity Export API.
#
# Run as a Vault ADMIN (export VAULT_ADDR and VAULT_TOKEN to an admin token -
# NOT a root token in production). Requires the `vault` CLI.
#
# Override any of the variables below via the environment, e.g.:
#   KUBECOST_NAMESPACE=kubecost KUBECOST_SA=kubecost-cost-analyzer ./vault-setup.sh
set -euo pipefail

KUBECOST_NAMESPACE="${KUBECOST_NAMESPACE:-kubecost}"
KUBECOST_SA="${KUBECOST_SA:-kubecost-cost-analyzer}"
VAULT_K8S_MOUNT="${VAULT_K8S_MOUNT:-kubernetes}"
VAULT_K8S_ROLE="${VAULT_K8S_ROLE:-kubecost-vault-cost}"
POLICY_NAME="${POLICY_NAME:-vault-cost-reader}"
TOKEN_TTL="${TOKEN_TTL:-15m}"
# For Vault running INSIDE the cluster this default works (Vault uses its own
# ServiceAccount as the token reviewer). For an EXTERNAL Vault, also set
# KUBERNETES_HOST to the cluster API URL and provide TOKEN_REVIEWER_JWT and
# KUBERNETES_CA_CERT (see the commented block below).
KUBERNETES_HOST="${KUBERNETES_HOST:-https://kubernetes.default.svc}"

echo ">> enabling '${VAULT_K8S_MOUNT}' auth (idempotent)"
vault auth enable "${VAULT_K8S_MOUNT}" 2>/dev/null || echo "   already enabled"

echo ">> configuring '${VAULT_K8S_MOUNT}' auth"
vault write "auth/${VAULT_K8S_MOUNT}/config" kubernetes_host="${KUBERNETES_HOST}"
# External Vault variant (uncomment and provide the values):
# vault write "auth/${VAULT_K8S_MOUNT}/config" \
#   kubernetes_host="${KUBERNETES_HOST}" \
#   token_reviewer_jwt="${TOKEN_REVIEWER_JWT}" \
#   kubernetes_ca_cert=@ca.crt

echo ">> writing policy '${POLICY_NAME}' (read + sudo; the export API is sudo-protected)"
vault policy write "${POLICY_NAME}" - <<'EOF'
path "sys/internal/counters/activity/export" { capabilities = ["read", "sudo"] }
path "sys/internal/counters/activity"        { capabilities = ["read", "sudo"] }
EOF

echo ">> creating role '${VAULT_K8S_ROLE}' bound to ${KUBECOST_NAMESPACE}/${KUBECOST_SA} (ttl ${TOKEN_TTL})"
vault write "auth/${VAULT_K8S_MOUNT}/role/${VAULT_K8S_ROLE}" \
  bound_service_account_names="${KUBECOST_SA}" \
  bound_service_account_namespaces="${KUBECOST_NAMESPACE}" \
  policies="${POLICY_NAME}" \
  ttl="${TOKEN_TTL}"

echo ">> done. Set the plugin config kubernetesRole=${VAULT_K8S_ROLE} (and kubernetesMount=${VAULT_K8S_MOUNT})."
