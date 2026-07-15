package vault

import (
	"os"
	"strings"
	"testing"
)

func TestParseNDJSON_File(t *testing.T) {
	f, err := os.Open("../../testdata/export_two_namespaces.ndjson")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	recs, err := ParseNDJSON(f)
	if err != nil {
		t.Fatal(err)
	}
	// 6 team-a + 1 duplicate + 4 team-b = 11 rows (dedup happens later).
	if len(recs) != 11 {
		t.Fatalf("want 11 records, got %d", len(recs))
	}
}

func TestParseNDJSON_SkipsBlankAndEmptyID(t *testing.T) {
	in := strings.Join([]string{
		`{"client_id":"a1","namespace_path":"team-a/","client_type":"entity"}`,
		``,
		`   `,
		`{"namespace_path":"team-a/","client_type":"entity"}`, // no client_id -> skipped
		`{"client_id":"a2","namespace_path":"team-a/"}`,
	}, "\n")
	recs, err := ParseNDJSON(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 2 {
		t.Fatalf("want 2 records, got %d", len(recs))
	}
}

func TestParseNDJSON_BadLine(t *testing.T) {
	if _, err := ParseNDJSON(strings.NewReader("{not json}")); err == nil {
		t.Fatal("expected error on malformed line")
	}
}

func TestNamespaceNormalization(t *testing.T) {
	cases := []struct {
		rec  ClientRecord
		want string
	}{
		{ClientRecord{NamespacePath: "team-a/"}, "team-a/"},
		{ClientRecord{NamespaceID: "root"}, "root"},
		{ClientRecord{}, "root"},
		{ClientRecord{NamespaceID: "nsid123"}, "nsid123"},
	}
	for _, c := range cases {
		if got := c.rec.Namespace(); got != c.want {
			t.Errorf("Namespace()=%q want %q", got, c.want)
		}
	}
}
