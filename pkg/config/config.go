// Package config loads and validates the vault plugin configuration and
// guarantees the token is never emitted in logs.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// CostModel selects the pricing strategy.
type CostModel string

const (
	ModelFullAllocation CostModel = "full_allocation"
	ModelPerClient      CostModel = "per_client"
)

// AuthMethod selects how the plugin authenticates to Vault.
type AuthMethod string

const (
	AuthKubernetes AuthMethod = "kubernetes" // preferred
	AuthToken      AuthMethod = "token"      // fallback: short-TTL periodic token
)

// Config is the on-disk plugin configuration (configs.vault in Kubecost Helm
// values, written to the plugin config dir by the OpenCost host).
type Config struct {
	// Vault connection.
	VaultAddress       string `json:"vaultAddress"`
	InsecureSkipVerify bool   `json:"insecureSkipVerify"` // skip TLS verify (dev/test only)

	// Authentication.
	AuthMethod       AuthMethod `json:"authMethod"`       // kubernetes | token
	KubernetesRole   string     `json:"kubernetesRole"`   // Vault role for k8s auth
	KubernetesMount  string     `json:"kubernetesMount"`  // default "kubernetes"
	ServiceTokenPath string     `json:"serviceTokenPath"` // SA JWT path
	// Token is only used for AuthMethod=token (fallback). Never logged.
	Token string `json:"token,omitempty"`

	// License / pricing.
	AnnualLicenseCost float64   `json:"annualLicenseCost"`
	LicensedClients   int       `json:"licensedClients"`
	Currency          string    `json:"currency"`
	CostModel         CostModel `json:"costModel"`

	// Vault namespace path -> team/cost-center. Fallback AccountName is "unmapped".
	NamespaceTeamMapping map[string]string `json:"namespaceTeamMapping"`

	// Operational.
	LogLevel          string  `json:"logLevel"`          // default "info"
	ExportRatePerSec  float64 `json:"exportRatePerSec"`  // rate limit for export calls
	ExportRateBurst   int     `json:"exportRateBurst"`   //
	RequestTimeoutSec int     `json:"requestTimeoutSec"` // per HTTP call
}

// configPathEnv is the environment variable the OpenCost host uses to point the
// plugin at its config file. A couple of fallbacks are honored for robustness.
func configFilePath() string {
	// OpenCost launches the plugin with the config file path as the first CLI
	// argument (e.g. /opt/opencost/plugin/config/vault_config.json). This is the
	// authoritative source and matches the reference plugins.
	if len(os.Args) > 1 && strings.TrimSpace(os.Args[1]) != "" {
		return os.Args[1]
	}
	// Fallback for local runs. NOTE: do NOT read Kubecost's CONFIG_PATH - it
	// points at Kubecost's own configs directory, not this plugin's config file.
	if v := os.Getenv("VAULT_CONFIG_PATH"); v != "" {
		return v
	}
	return "/opt/opencost/plugin/config/vault_config.json"
}

// Load reads, parses and validates the configuration.
func Load() (*Config, error) {
	path := configFilePath()
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("parsing config %s: %w", path, err)
	}
	c.applyDefaults()
	if err := c.validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) applyDefaults() {
	if c.KubernetesMount == "" {
		c.KubernetesMount = "kubernetes"
	}
	if c.ServiceTokenPath == "" {
		c.ServiceTokenPath = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	}
	if c.AuthMethod == "" {
		c.AuthMethod = AuthKubernetes
	}
	if c.CostModel == "" {
		c.CostModel = ModelFullAllocation
	}
	if c.Currency == "" {
		c.Currency = "USD"
	}
	if c.LogLevel == "" {
		c.LogLevel = "info"
	}
	if c.ExportRatePerSec == 0 {
		c.ExportRatePerSec = 0.2 // 1 call / 5s
	}
	if c.ExportRateBurst == 0 {
		c.ExportRateBurst = 1
	}
	if c.RequestTimeoutSec == 0 {
		c.RequestTimeoutSec = 60
	}
	if c.NamespaceTeamMapping == nil {
		c.NamespaceTeamMapping = map[string]string{}
	}
}

func (c *Config) validate() error {
	if c.VaultAddress == "" {
		return fmt.Errorf("vaultAddress is required")
	}
	if c.CostModel != ModelFullAllocation && c.CostModel != ModelPerClient {
		return fmt.Errorf("costModel must be %q or %q", ModelFullAllocation, ModelPerClient)
	}
	if c.AnnualLicenseCost <= 0 {
		return fmt.Errorf("annualLicenseCost must be > 0")
	}
	if c.CostModel == ModelPerClient && c.LicensedClients <= 0 {
		return fmt.Errorf("licensedClients must be > 0 for per_client model")
	}
	switch c.AuthMethod {
	case AuthKubernetes:
		if c.KubernetesRole == "" {
			return fmt.Errorf("kubernetesRole is required for kubernetes auth")
		}
	case AuthToken:
		if c.Token == "" {
			return fmt.Errorf("token is required for token auth")
		}
	default:
		return fmt.Errorf("unknown authMethod %q", c.AuthMethod)
	}
	return nil
}

// TeamFor maps a Vault namespace path to a team/cost-center, falling back to
// "unmapped". Matching is exact first, then trimmed-slash tolerant.
func (c *Config) TeamFor(namespace string) string {
	if v, ok := c.NamespaceTeamMapping[namespace]; ok {
		return v
	}
	// tolerate trailing-slash differences (e.g. "team-a/" vs "team-a")
	trimmed := strings.TrimSuffix(namespace, "/")
	for k, v := range c.NamespaceTeamMapping {
		if strings.TrimSuffix(k, "/") == trimmed {
			return v
		}
	}
	return "unmapped"
}

// Redacted returns a log-safe, single-line rendering that never exposes the
// token field.
func (c *Config) Redacted() string {
	clone := *c
	if clone.Token != "" {
		clone.Token = "***REDACTED***"
	}
	b, _ := json.Marshal(clone)
	return string(b)
}
