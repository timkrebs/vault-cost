# vault-cost

A Kubecost / OpenCost custom-cost plugin that turns HashiCorp Vault Enterprise
client counts into per-namespace license cost, shown in Kubecost's External
Costs view under the `vault` domain.

Vault Enterprise is licensed by client count, but that spend usually arrives as
a single number with no idea of which team drove it. This plugin reads Vault's
client activity, groups it by Vault namespace, applies a pricing model you pick,
and reports it to Kubecost so it sits next to the rest of your infrastructure
cost.

## What it does

- Reads client activity from Vault's Activity Export API
  (`sys/internal/counters/activity/export`).
- De-duplicates by `client_id` and groups clients by Vault namespace.
- Prices each namespace (`per_client` or `full_allocation`).
- Emits one cost record per namespace, tagged with a cost center you map.

## Requirements

- Kubecost **2.x** (the `cost-analyzer` chart). The plugin interface used here
  was removed in Kubecost 3.x.
- Vault Enterprise with client counting enabled (on by default).
- To build and deploy: Go 1.22+, Docker (buildx), helm, kubectl, the vault CLI,
  and a container registry your cluster can pull from.

## Build

```sh
make tidy test
make image IMAGE=<registry>/vault-cost:1.0.0   # buildx; push to your registry
```

The plugin links against OpenCost's plugin SDK. If the gRPC handshake fails at
runtime, pin `github.com/opencost/opencost/core` in `go.mod` to the version your
Kubecost image ships and rebuild.

## Vault access

The plugin uses Vault's Kubernetes auth method and needs `read` on the two
activity counters (the export endpoint is sudo-protected, so `sudo` is required
too). `deploy/vault-setup.sh` handles it — run it against your Vault as an admin:

```sh
KUBECOST_NAMESPACE=kubecost KUBECOST_SA=kubecost-cost-analyzer ./deploy/vault-setup.sh
```

It enables Kubernetes auth, writes the policy, and binds a role to the
cost-analyzer service account with a 15 minute token TTL. No root token is used
at runtime.

## Configure

All settings are one JSON object — `configs.vault` in the Helm overlay, or
`deploy/plugin-config/vault.json` as the image default.

| Field | Meaning |
|-------|---------|
| `vaultAddress` | Vault URL reachable from the kubecost namespace |
| `authMethod` | `kubernetes` (default) or `token` |
| `kubernetesRole`, `kubernetesMount` | role/mount created by `vault-setup.sh` |
| `token` | only for `authMethod: token`; use a short-TTL token, never root |
| `annualLicenseCost` | your yearly Vault license spend |
| `licensedClients` | number of licensed clients |
| `currency` | ISO code shown in Kubecost (`USD`, `EUR`, ...) |
| `costModel` | `per_client` or `full_allocation` |
| `namespaceTeamMapping` | Vault namespace path to cost center; unmatched → `unmapped` |
| `insecureSkipVerify` | skip TLS verification, dev/test only |
| `logLevel`, `exportRatePerSec`, `requestTimeoutSec` | operational tuning |

### Pricing models

`per_client` — each active client costs `annualLicenseCost / licensedClients`
per year, charged monthly. So `100000` for `120` clients works out to 833.33 per
client per year; change either number and everything re-prices on the next
ingest. The total tracks real usage.

`full_allocation` — the whole `annualLicenseCost / 12` is split across
namespaces by their share of clients each month. The total is fixed whether you
use 10 clients or 1000.

Either way costs are prorated to the reporting window.

## Deploy

```sh
# enable the plugin framework + push your config
helm upgrade <release> kubecost/cost-analyzer -n <ns> \
  -f <your-values.yaml> -f deploy/kubecost-plugin-values.yaml

# ship the binary (set your image in the patch first)
kubectl -n <ns> patch deployment <cost-analyzer> \
  --type=strategic --patch-file deploy/kubecost-cost-analyzer-plugin-patch.yaml

kubectl -n <ns> rollout restart deploy/<cost-analyzer>
```

Kubecost's built-in installer only downloads official plugins, so the patch
replaces its init container with this image, which drops the binary into the
shared plugin directory. Re-apply the patch after any `helm upgrade` — it resets
the init container.

## Verify

In the UI: Monitor → External Costs, filter Domain = `vault`. Or query the API:

```sh
curl "http://<cost-analyzer>/model/customCost/total?window=month&aggregate=resourceName"
```

## Notes

- A Vault client only counts once it has activity in the period, and the export
  API surfaces it after Vault persists an activity segment (roughly every 10
  minutes), so freshly active clients show up with a short delay.
- The plugin runs inside the cost-analyzer `cloud-cost` container; OpenCost
  starts it with the config file path as the first argument.
- Enable Vault TLS and leave `insecureSkipVerify` off outside of testing.

## License

[MIT](LICENSE).
