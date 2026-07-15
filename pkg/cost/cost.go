// Package cost turns Vault Activity Export records into FOCUS-conformant
// OpenCost CustomCost records: dedup by client_id, group by namespace, price
// per the configured model, and emit one CustomCost per namespace.
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

		counts, order := dedupByNamespace(recs)
		prices := pricer.Price(counts, start, end)
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

// dedupByNamespace counts distinct client_ids per namespace. A client_id is
// counted exactly once (first occurrence wins). Returns counts and a sorted
// namespace order for deterministic output.
func dedupByNamespace(recs []vault.ClientRecord) (map[string]int, []string) {
	seen := make(map[string]struct{}, len(recs))
	counts := map[string]int{}
	for _, r := range recs {
		if _, dup := seen[r.ClientID]; dup {
			continue
		}
		seen[r.ClientID] = struct{}{}
		counts[r.Namespace()]++
	}
	order := make([]string, 0, len(counts))
	for ns := range counts {
		order = append(order, ns)
	}
	sort.Strings(order)
	return counts, order
}

// Price returns namespace -> BilledCost for the window, prorated to the window's
// fraction of its calendar month.
func (p *Pricer) Price(counts map[string]int, start, end time.Time) map[string]float64 {
	frac := monthFraction(start, end)
	if p.cfg.CostModel == config.ModelPerClient {
		perClient := p.cfg.AnnualLicenseCost / float64(p.cfg.LicensedClients) / 12.0 * frac
		res := make(map[string]float64, len(counts))
		for ns, c := range counts {
			res[ns] = roundCents(float64(c) * perClient)
		}
		return res
	}
	// full_allocation: split the (prorated) monthly bucket across namespaces by
	// their share of distinct clients, summing exactly to the bucket.
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
func splitBucket(bucket float64, counts map[string]int) map[string]float64 {
	res := map[string]float64{}
	total := 0
	for _, c := range counts {
		total += c
	}
	if total == 0 || bucket <= 0 {
		for ns := range counts {
			res[ns] = 0
		}
		return res
	}
	cents := int64(math.Round(bucket * 100))

	type rem struct {
		ns  string
		rem float64
	}
	rems := make([]rem, 0, len(counts))
	var allocated int64
	for ns, c := range counts {
		exact := float64(cents) * float64(c) / float64(total)
		fl := int64(math.Floor(exact))
		res[ns] = float64(fl) / 100.0
		allocated += fl
		rems = append(rems, rem{ns, exact - float64(fl)})
	}
	// Assign leftover cents to the largest remainders (ties broken by name).
	sort.Slice(rems, func(i, j int) bool {
		if rems[i].rem != rems[j].rem {
			return rems[i].rem > rems[j].rem
		}
		return rems[i].ns < rems[j].ns
	})
	for i := int64(0); i < cents-allocated && len(rems) > 0; i++ {
		ns := rems[i%int64(len(rems))].ns
		res[ns] = roundCents(res[ns] + 0.01)
	}
	return res
}

func roundCents(x float64) float64 { return math.Round(x*100) / 100 }

func buildResponse(cfg *config.Config, start, end time.Time, counts map[string]int, order []string, prices map[string]float64) *pb.CustomCostResponse {
	costs := make([]*pb.CustomCost, 0, len(order))
	for _, ns := range order {
		cost := prices[ns]
		costs = append(costs, &pb.CustomCost{
			AccountName:    cfg.TeamFor(ns),
			ChargeCategory: "License",
			Description:    "Vault Enterprise distinct client license allocation",
			ResourceName:   ns,
			ResourceType:   "vault-clients",
			Id:             ns,
			ProviderId:     "vault/" + ns,
			Labels:         map[string]string{"vault_namespace": ns},
			ListCost:       float32(cost),
			ListUnitPrice:  0,
			BilledCost:     float32(cost),
			UsageQuantity:  float32(counts[ns]),
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
