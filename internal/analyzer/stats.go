package analyzer

import (
	"fmt"
	"math"
	"sort"
)

// ErrorKey identifies one distinct kind of error for the "top errors"
// table — status code plus message, since "500 database timeout" and
// "500 connection refused" are very different problems that happen to
// share a status code.
type ErrorKey struct {
	Status  int
	Message string
}

// Stats accumulates everything needed to reproduce the summary shown in
// the README example (totals, error rate, latency percentiles, top
// errors) plus optional grouping by an arbitrary field.
type Stats struct {
	Total     int
	Errors    int
	latencies []float64
	errorCnt  map[ErrorKey]int
	groups    map[string]int
	groupBy   string
}

func NewStats(groupBy string) *Stats {
	return &Stats{
		errorCnt: make(map[ErrorKey]int),
		groups:   make(map[string]int),
		groupBy:  groupBy,
	}
}

// isError treats HTTP-style 4xx/5xx as errors when a status is present;
// entries with no status at all (status == 0) are never counted as
// errors, since plenty of log lines simply don't carry one.
func isError(status int) bool {
	return status >= 400
}

func (s *Stats) Add(e Entry) {
	s.Total++
	if e.LatencyMs > 0 {
		s.latencies = append(s.latencies, e.LatencyMs)
	}
	if isError(e.Status) {
		s.Errors++
		key := ErrorKey{Status: e.Status, Message: truncate(e.Message, 60)}
		s.errorCnt[key]++
	}
	if s.groupBy != "" {
		if v, ok := e.Raw[s.groupBy]; ok {
			s.groups[fmt.Sprintf("%v", v)]++
		} else {
			s.groups["(missing)"]++
		}
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// Summary is the final, render-ready result.
type Summary struct {
	Total     int              `json:"total_requests"`
	Errors    int              `json:"errors"`
	ErrorRate float64          `json:"error_rate"`
	P50Ms     float64          `json:"p50_ms"`
	P95Ms     float64          `json:"p95_ms"`
	P99Ms     float64          `json:"p99_ms"`
	TopErrors []TopError       `json:"top_errors,omitempty"`
	Groups    []GroupCount     `json:"groups,omitempty"`
}

type TopError struct {
	Status  int    `json:"status"`
	Message string `json:"message"`
	Count   int    `json:"count"`
}

type GroupCount struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

func (s *Stats) Summary(topN int) Summary {
	sorted := append([]float64(nil), s.latencies...)
	sort.Float64s(sorted)

	sum := Summary{
		Total:  s.Total,
		Errors: s.Errors,
	}
	if s.Total > 0 {
		sum.ErrorRate = float64(s.Errors) / float64(s.Total) * 100
	}
	sum.P50Ms = percentile(sorted, 50)
	sum.P95Ms = percentile(sorted, 95)
	sum.P99Ms = percentile(sorted, 99)

	type kv struct {
		k ErrorKey
		v int
	}
	errs := make([]kv, 0, len(s.errorCnt))
	for k, v := range s.errorCnt {
		errs = append(errs, kv{k, v})
	}
	sort.Slice(errs, func(i, j int) bool { return errs[i].v > errs[j].v })
	if topN > 0 && len(errs) > topN {
		errs = errs[:topN]
	}
	for _, e := range errs {
		sum.TopErrors = append(sum.TopErrors, TopError{Status: e.k.Status, Message: e.k.Message, Count: e.v})
	}

	if s.groupBy != "" {
		type gkv struct {
			k string
			v int
		}
		gs := make([]gkv, 0, len(s.groups))
		for k, v := range s.groups {
			gs = append(gs, gkv{k, v})
		}
		sort.Slice(gs, func(i, j int) bool { return gs[i].v > gs[j].v })
		for _, g := range gs {
			sum.Groups = append(sum.Groups, GroupCount{Value: g.k, Count: g.v})
		}
	}

	return sum
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(math.Ceil(p/100*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}
