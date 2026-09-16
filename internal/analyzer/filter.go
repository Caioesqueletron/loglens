package analyzer

import (
	"fmt"
	"strings"
	"time"
)

// Filter narrows down which entries are counted towards the final
// statistics. All conditions are ANDed together.
type Filter struct {
	Statuses map[int]bool     // empty = match any status
	Since    time.Time        // zero = no lower bound
	Fields   map[string]string // arbitrary raw-field equality checks
}

// ParseSince turns a duration like "1h", "15m", "24h" into an absolute
// cutoff time relative to now, matching the `--last 1h` CLI flag.
func ParseSince(last string) (time.Time, error) {
	if last == "" {
		return time.Time{}, nil
	}
	d, err := time.ParseDuration(last)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid --last value %q: %w", last, err)
	}
	return time.Now().Add(-d), nil
}

// ParseFieldFilters turns ["env=prod", "service=api"] into a map, as
// produced by repeated `--field key=value` flags.
func ParseFieldFilters(raw []string) (map[string]string, error) {
	out := make(map[string]string, len(raw))
	for _, kv := range raw {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid --field %q, expected key=value", kv)
		}
		out[parts[0]] = parts[1]
	}
	return out, nil
}

// Match reports whether the entry satisfies every configured condition.
func (f Filter) Match(e Entry) bool {
	if len(f.Statuses) > 0 && !f.Statuses[e.Status] {
		return false
	}
	if !f.Since.IsZero() && e.Timestamp.Before(f.Since) {
		return false
	}
	for k, want := range f.Fields {
		got, ok := e.Raw[k]
		if !ok {
			return false
		}
		if fmt.Sprintf("%v", got) != want {
			return false
		}
	}
	return true
}
