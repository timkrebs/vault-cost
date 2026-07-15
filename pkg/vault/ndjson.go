package vault

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// ParseNDJSON parses the newline-delimited JSON body of the Activity Export
// API into client records. Blank lines are skipped; records without a
// client_id are ignored (defensive).
func ParseNDJSON(r io.Reader) ([]ClientRecord, error) {
	var out []ClientRecord
	sc := bufio.NewScanner(r)
	// Activity export lines can be large; allow up to 4MiB per line.
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	line := 0
	for sc.Scan() {
		line++
		b := bytes.TrimSpace(sc.Bytes())
		if len(b) == 0 {
			continue
		}
		var rec ClientRecord
		if err := json.Unmarshal(b, &rec); err != nil {
			return nil, fmt.Errorf("ndjson line %d: %w", line, err)
		}
		if rec.ClientID == "" {
			continue
		}
		out = append(out, rec)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scanning ndjson: %w", err)
	}
	return out, nil
}
