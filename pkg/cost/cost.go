// Package cost turns Vault Activity Export records into FOCUS-conformant
// OpenCost CustomCost records: dedup by client_id, group by namespace and
// client type, price per the configured model, and emit one CustomCost per
// (namespace, client type).
package cost

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/opencost/opencost/core/pkg/log"
	"github.com/opencost/opencost/core/pkg/model/pb"
	"github.com/opencost/opencost/core/pkg/opencost"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/timkrebs/vault-cost/pkg/config"
	"github.com/timkrebs/vault-cost/pkg/vault"
)

// Exporter is the subset of the Vault client used here (injectable for tests).
type Exporter interface {
	ExportClients(ctx context.Context, start, end time.Time) ([]vault.ClientRecord, error)
}

// Pricer applies the configured pricing model.
type Pricer struct{ cfg *config.Config }

// NewPricer builds a Pricer.
func NewPricer(cfg *config.Config) *Pricer { return &Pricer{cfg: cfg} }

// WindowCache memoizes per-window responses so OpenCost re-queries after a pod
// restart are cheap and idempotent.
type WindowCache struct {
	mu sync.RWMutex
	m  map[string]*pb.CustomCostResponse
}

// NewWindowCache builds an empty cache.
func NewWindowCache() *WindowCache { return &WindowCache{m: map[string]*pb.CustomCostResponse{}} }

func (c *WindowCache) Get(k string) (*pb.CustomCostResponse, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	v, ok := c.m[k]
	return v, ok
}

func (c *WindowCache) Set(k string, v *pb.CustomCostResponse) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[k] = v
}

// GetCustomCosts is the plugin entrypoint: one response per requested window.
func GetCustomCosts(client Exporter, pricer *Pricer, cache *WindowCache, cfg *config.Config, req *pb.CustomCostRequest) []*pb.CustomCostResponse {
	results := []*pb.CustomCostResponse{}

	windows, err := opencost.GetWindows(req.Start.AsTime(), req.End.AsTime(), req.Resolution.AsDuration())
	if err != nil {
		return []*pb.CustomCostResponse{{Errors: []string{fmt.Sprintf("computing windows: %v", err)}}}
	}

	for _, w := range windows {
		key := w.String()
		if cached, ok := cache.Get(key); ok {
			results = append(results, cached)
			continue
		}
		start, end := *w.Start(), *w.End()
		// Vault cannot report the future.
		if start.After(time.Now().UTC()) {
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.RequestTimeoutSec+5)*time.Second)
		recs, err := client.ExportClients(ctx, start, end)
		cancel()
		if err != nil {
			log.Errorf("vault plugin: export for %s: %v", key, err)
			results = append(results, errorResponse(cfg, start, end, err))
			continue
		}

		counts, order := dedupClients(recs)
		prices := pricer.price(counts, start, end)
		resp := buildResponse(cfg, start, end, counts, order, prices)

		// Cache only CLOSED windows that produced cost records. Open (in-progress)
		// windows and empty results are always re-queried, so late-arriving Vault
		// activity (e.g. an activity-log segment flush) is picked up instead of
		// being pinned to zero.
		if end.Before(time.Now().UTC()) && len(resp.Costs) > 0 {
			cache.Set(key, resp)
		}
		results = append(results, resp)
	}
	return results
}

// clientKey groups distinct clients by Vault namespace and client type. Vault
// counts entity, non-entity, acme, and secret-sync clients separately, so they
// are reported as separate cost lines.
type clientKey struct {
	ns    string
	ctype string
}

func (k clientKey) String() string { return k.ns + "|" + k.ctype }

// dedupClients counts distinct client_ids per (namespace, client type). A
// client_id is counted once (first occurrence wins). Returns the counts and a
// stable, sorted key order for deterministic output.
func dedupClients(recs []vault.ClientRecord) (map[clientKey]int, []clientKey) {
	seen := make(map[string]struct{}, len(recs))
	counts := map[clientKey]int{}
	for _, r := range recs {
		if _, dup := seen[r.ClientID]; dup {
			continue
		}
		seen[r.ClientID] = struct{}{}
		counts[clientKey{r.Namespace(), r.ClientTypeLabel()}]++
	}
	order := make([]clientKey, 0, len(counts))
	for k := range counts {
		order = append(order, k)
	}
	sort.Slice(order, func(i, j int) bool { return order[i].String() < order[j].String() })
	return counts, order
}

// Price returns namespace -> BilledCost for the window, prorated to the window's
// fraction of its calendar month.
func (p *Pricer) price(counts map[clientKey]int, start, end time.Time) map[clientKey]float64 {
	frac := monthFraction(start, end)
	if p.cfg.CostModel == config.ModelPerClient {
		perClient := p.cfg.AnnualLicenseCost / float64(p.cfg.LicensedClients) / 12.0 * frac
		res := make(map[clientKey]float64, len(counts))
		for k, c := range counts {
			res[k] = roundCents(float64(c) * perClient)
		}
		return res
	}
	// full_allocation: split the (prorated) monthly bucket across all
	// (namespace, type) buckets by their share of distinct clients, summing
	// exactly to the bucket.
	bucket := p.cfg.AnnualLicenseCost / 12.0 * frac
	return splitBucket(bucket, counts)
}

// monthFraction is the fraction of the calendar month (of start) spanned by
// [start,end). A full calendar-month window yields exactly 1.0.
func monthFraction(start, end time.Time) float64 {
	start, end = start.UTC(), end.UTC()
	if !end.After(start) {
		return 0
	}
	monthStart := time.Date(start.Year(), start.Month(), 1, 0, 0, 0, 0, time.UTC)
	monthDur := monthStart.AddDate(0, 1, 0).Sub(monthStart).Seconds()
	return end.Sub(start).Seconds() / monthDur
}

// splitBucket distributes bucket across namespaces by client share, using the
// largest-remainder method in whole cents so the parts sum exactly to bucket.
func splitBucket(bucket float64, counts map[clientKey]int) map[clientKey]float64 {
	res := map[clientKey]float64{}
	total := 0
	for _, c := range counts {
		total += c
	}
	if total == 0 || bucket <= 0 {
		for k := range counts {
			res[k] = 0
		}
		return res
	}
	cents := int64(math.Round(bucket * 100))

	type rem struct {
		key clientKey
		rem float64
	}
	rems := make([]rem, 0, len(counts))
	var allocated int64
	for k, c := range counts {
		exact := float64(cents) * float64(c) / float64(total)
		fl := int64(math.Floor(exact))
		res[k] = float64(fl) / 100.0
		allocated += fl
		rems = append(rems, rem{k, exact - float64(fl)})
	}
	// Assign leftover cents to the largest remainders (ties broken by key).
	sort.Slice(rems, func(i, j int) bool {
		if rems[i].rem != rems[j].rem {
			return rems[i].rem > rems[j].rem
		}
		return rems[i].key.String() < rems[j].key.String()
	})
	for i := int64(0); i < cents-allocated && len(rems) > 0; i++ {
		k := rems[i%int64(len(rems))].key
		res[k] = roundCents(res[k] + 0.01)
	}
	return res
}

func roundCents(x float64) float64 { return math.Round(x*100) / 100 }

func buildResponse(cfg *config.Config, start, end time.Time, counts map[clientKey]int, order []clientKey, prices map[clientKey]float64) *pb.CustomCostResponse {
	costs := make([]*pb.CustomCost, 0, len(order))
	for _, k := range order {
		cost := prices[k]
		costs = append(costs, &pb.CustomCost{
			AccountName:    cfg.TeamFor(k.ns),
			ChargeCategory: "License",
			Description:    "Vault Enterprise " + k.ctype + " client license allocation",
			ResourceName:   k.ns,
			ResourceType:   "vault-clients-" + k.ctype,
			Id:             k.ns + "/" + k.ctype,
			ProviderId:     "vault/" + k.ns + "/" + k.ctype,
			Labels:         map[string]string{"vault_namespace": k.ns, "client_type": k.ctype},
			ListCost:       float32(cost),
			ListUnitPrice:  0,
			BilledCost:     float32(cost),
			UsageQuantity:  float32(counts[k]),
			UsageUnit:      "clients",
		})
	}
	return &pb.CustomCostResponse{
		Metadata:   map[string]string{"plugin": "vault", "cost_model": string(cfg.CostModel)},
		CostSource: "vault",
		Domain:     "vault",
		Version:    "v1",
		Currency:   cfg.Currency,
		Start:      timestamppb.New(start),
		End:        timestamppb.New(end),
		Errors:     []string{},
		Costs:      costs,
	}
}

func errorResponse(cfg *config.Config, start, end time.Time, err error) *pb.CustomCostResponse {
	return &pb.CustomCostResponse{
		Domain:   "vault",
		Currency: cfg.Currency,
		Start:    timestamppb.New(start),
		End:      timestamppb.New(end),
		Errors:   []string{err.Error()},
		Costs:    []*pb.CustomCost{},
	}
}
