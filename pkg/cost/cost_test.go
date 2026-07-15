package cost

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/opencost/opencost/core/pkg/model/pb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/timkrebs/vault-cost/pkg/config"
	"github.com/timkrebs/vault-cost/pkg/vault"
)

var (
	sept1 = time.Date(2025, 9, 1, 0, 0, 0, 0, time.UTC)
	oct1  = time.Date(2025, 10, 1, 0, 0, 0, 0, time.UTC)
)

func testCfg(model config.CostModel) *config.Config {
	return &config.Config{
		VaultAddress:         "http://vault.vault:8200",
		AuthMethod:           config.AuthToken,
		Token:                "unused-in-tests",
		AnnualLicenseCost:    120000,
		LicensedClients:      1000,
		Currency:             "EUR",
		CostModel:            model,
		NamespaceTeamMapping: map[string]string{"team-a/": "CC-1001", "team-b/": "CC-1002", "root": "platform"},
		RequestTimeoutSec:    10,
	}
}

func loadRecs(t *testing.T) []vault.ClientRecord {
	t.Helper()
	f, err := os.Open("../../testdata/export_two_namespaces.ndjson")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	recs, err := vault.ParseNDJSON(f)
	if err != nil {
		t.Fatal(err)
	}
	return recs
}

type projCost struct {
	Namespace string  `json:"namespace"`
	Type      string  `json:"type"`
	Account   string  `json:"account"`
	Usage     int     `json:"usage"`
	Unit      string  `json:"unit"`
	Billed    float64 `json:"billed"`
}

type proj struct {
	Currency string     `json:"currency"`
	Domain   string     `json:"domain"`
	Costs    []projCost `json:"costs"`
}

func project(r *pb.CustomCostResponse) proj {
	p := proj{Currency: r.Currency, Domain: r.Domain}
	for _, c := range r.Costs {
		p.Costs = append(p.Costs, projCost{
			Namespace: c.ResourceName,
			Type:      c.Labels["client_type"],
			Account:   c.AccountName,
			Usage:     int(c.UsageQuantity),
			Unit:      c.UsageUnit,
			Billed:    math.Round(float64(c.BilledCost)*100) / 100,
		})
	}
	return p
}

func loadGolden(t *testing.T, name string) proj {
	t.Helper()
	b, err := os.ReadFile("../../testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	var p proj
	if err := json.Unmarshal(b, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func buildForModel(t *testing.T, model config.CostModel) *pb.CustomCostResponse {
	cfg := testCfg(model)
	counts, order := dedupClients(loadRecs(t))
	prices := NewPricer(cfg).price(counts, sept1, oct1)
	return buildResponse(cfg, sept1, oct1, counts, order, prices)
}

func TestDedupClients(t *testing.T) {
	counts, order := dedupClients(loadRecs(t))
	want := map[clientKey]int{
		{"team-a/", "entity"}:     4,
		{"team-a/", "acme"}:       1,
		{"team-b/", "entity"}:     3,
		{"team-b/", "non-entity"}: 1,
		{"team-b/", "acme"}:       1,
	}
	if !reflect.DeepEqual(counts, want) {
		t.Errorf("counts mismatch:\n got=%v\nwant=%v", counts, want)
	}
	wantOrder := []clientKey{
		{"team-a/", "acme"},
		{"team-a/", "entity"},
		{"team-b/", "acme"},
		{"team-b/", "entity"},
		{"team-b/", "non-entity"},
	}
	if !reflect.DeepEqual(order, wantOrder) {
		t.Errorf("order not sorted: %v", order)
	}
}

func TestGolden_FullAllocation(t *testing.T) {
	got := project(buildForModel(t, config.ModelFullAllocation))
	if want := loadGolden(t, "golden_full_allocation.json"); !reflect.DeepEqual(got, want) {
		t.Fatalf("full_allocation mismatch:\n got=%+v\nwant=%+v", got, want)
	}
}

func TestGolden_PerClient(t *testing.T) {
	got := project(buildForModel(t, config.ModelPerClient))
	if want := loadGolden(t, "golden_per_client.json"); !reflect.DeepEqual(got, want) {
		t.Fatalf("per_client mismatch:\n got=%+v\nwant=%+v", got, want)
	}
}

func TestFullAllocationSumsToBucket(t *testing.T) {
	r := buildForModel(t, config.ModelFullAllocation)
	var sum float64
	for _, c := range r.Costs {
		sum += float64(c.BilledCost)
	}
	if math.Abs(sum-10000) > 0.001 {
		t.Fatalf("full_allocation month total = %.4f, want 10000 (ANNUAL/12)", sum)
	}
}

func TestMappingFallback(t *testing.T) {
	cfg := testCfg(config.ModelFullAllocation)
	if got := cfg.TeamFor("team-z/"); got != "unmapped" {
		t.Fatalf("unmapped namespace: got %q want unmapped", got)
	}
	if got := cfg.TeamFor("team-a/"); got != "CC-1001" {
		t.Fatalf("mapped namespace: got %q want CC-1001", got)
	}
}

type fakeExporter struct {
	recs   []vault.ClientRecord
	err    error
	called int
}

func (f *fakeExporter) ExportClients(ctx context.Context, s, e time.Time) ([]vault.ClientRecord, error) {
	f.called++
	return f.recs, f.err
}

func monthlyReq() *pb.CustomCostRequest {
	// Daily resolution: 30 windows spanning September (evenly divisible, which
	// opencost.GetWindows requires). The host sends aligned windows in practice.
	return &pb.CustomCostRequest{
		Start:      timestamppb.New(sept1),
		End:        timestamppb.New(oct1),
		Resolution: durationpb.New(24 * time.Hour),
	}
}

func TestGetCustomCosts_204Empty(t *testing.T) {
	cfg := testCfg(config.ModelFullAllocation)
	f := &fakeExporter{recs: nil, err: nil} // 204 => (nil, nil)
	res := GetCustomCosts(f, NewPricer(cfg), NewWindowCache(), cfg, monthlyReq())
	if len(res) == 0 {
		t.Fatal("expected at least one response")
	}
	for _, r := range res {
		if len(r.Errors) != 0 {
			t.Fatalf("unexpected errors: %v", r.Errors)
		}
		if len(r.Costs) != 0 {
			t.Fatalf("expected empty costs for 204, got %d", len(r.Costs))
		}
	}
}

func TestGetCustomCosts_Caches(t *testing.T) {
	cfg := testCfg(config.ModelFullAllocation)
	f := &fakeExporter{recs: loadRecs(t)}
	cache := NewWindowCache()
	_ = GetCustomCosts(f, NewPricer(cfg), cache, cfg, monthlyReq())
	first := f.called
	if first == 0 {
		t.Fatal("exporter should have been called at least once")
	}
	_ = GetCustomCosts(f, NewPricer(cfg), cache, cfg, monthlyReq())
	if f.called != first {
		t.Fatalf("expected cache hits on 2nd call; exporter called %d then %d", first, f.called)
	}
}
