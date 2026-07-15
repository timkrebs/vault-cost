// Command vault is a Kubecost/OpenCost custom-cost plugin. It prices Vault
// Enterprise clients per Vault namespace and reports the result to Kubecost's
// External Costs.
package main

import (
	"github.com/hashicorp/go-plugin"
	"github.com/opencost/opencost/core/pkg/log"
	"github.com/opencost/opencost/core/pkg/model/pb"
	ocplugin "github.com/opencost/opencost/core/pkg/plugin"

	"github.com/timkrebs/vault-cost/pkg/config"
	"github.com/timkrebs/vault-cost/pkg/cost"
	"github.com/timkrebs/vault-cost/pkg/vault"
)

// The host and plugin must agree on this handshake or the plugin is rejected;
// MagicCookieValue is the plugin name.
var handshakeConfig = plugin.HandshakeConfig{
	ProtocolVersion:  1,
	MagicCookieKey:   "PLUGIN_NAME",
	MagicCookieValue: "vault",
}

// VaultCostSource implements the OpenCost CustomCostSource interface.
type VaultCostSource struct {
	cfg    *config.Config
	client *vault.Client
	pricer *cost.Pricer
	cache  *cost.WindowCache
}

// GetCustomCosts is invoked by the OpenCost host for each requested window set.
func (s *VaultCostSource) GetCustomCosts(req *pb.CustomCostRequest) []*pb.CustomCostResponse {
	return cost.GetCustomCosts(s.client, s.pricer, s.cache, s.cfg, req)
}

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("vault plugin: loading config: %v", err)
	}
	if err := log.SetLogLevel(cfg.LogLevel); err != nil {
		log.Warnf("vault plugin: invalid logLevel %q, using default: %v", cfg.LogLevel, err)
	}
	// Redacted() never includes the token.
	log.Infof("vault plugin starting with config: %s", cfg.Redacted())

	client, err := vault.NewClient(cfg)
	if err != nil {
		log.Fatalf("vault plugin: building vault client: %v", err)
	}

	src := &VaultCostSource{
		cfg:    cfg,
		client: client,
		pricer: cost.NewPricer(cfg),
		cache:  cost.NewWindowCache(),
	}

	pluginMap := map[string]plugin.Plugin{
		"CustomCostSource": &ocplugin.CustomCostPlugin{Impl: src},
	}

	plugin.Serve(&plugin.ServeConfig{
		HandshakeConfig: handshakeConfig,
		Plugins:         pluginMap,
		GRPCServer:      plugin.DefaultGRPCServer,
	})
}
