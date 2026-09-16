package analyzer

import (
	"encoding/json"
	"strconv"
	"time"
)

// Entry is a normalized view over one JSON log line. Applications log
// wildly different schemas, so we pull out a handful of well-known
// fields (status, latency, timestamp, message) on a best-effort basis
// and keep everything else in Raw for arbitrary field filtering /
// grouping.
type Entry struct {
	Timestamp time.Time
	Status    int
	LatencyMs float64
	Message   string
	Raw       map[string]interface{}
}

// commonTimeLayouts covers the timestamp formats seen in the wild across
// popular loggers (RFC3339/Zap/Logrus/nginx-json/etc).
var commonTimeLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02T15:04:05.000Z0700",
	"2006-01-02 15:04:05",
}

// ParseLine decodes one JSON log line into an Entry. Lines that aren't
// valid JSON are returned as an Entry with only Message set (so a mixed
// plain-text/JSON log file degrades gracefully instead of erroring out).
func ParseLine(line []byte) (Entry, error) {
	var raw map[string]interface{}
	if err := json.Unmarshal(line, &raw); err != nil {
		return Entry{Message: string(line)}, err
	}

	e := Entry{Raw: raw}

	if v, ok := firstString(raw, "status", "status_code", "statusCode", "code"); ok {
		if n, err := strconv.Atoi(v); err == nil {
			e.Status = n
		}
	} else if v, ok := firstNumber(raw, "status", "status_code", "statusCode", "code"); ok {
		e.Status = int(v)
	}

	if v, ok := firstNumber(raw, "latency_ms", "latencyMs", "duration_ms", "durationMs", "response_time_ms"); ok {
		e.LatencyMs = v
	} else if v, ok := firstNumber(raw, "latency", "duration"); ok {
		// Heuristic: values under 100 with no _ms suffix are probably
		// already seconds (e.g. Go's time.Since().Seconds()); larger raw
		// numbers are probably already milliseconds. Not perfect, but
		// good enough for a CLI summarizer, and documented in README.
		if v < 100 {
			e.LatencyMs = v * 1000
		} else {
			e.LatencyMs = v
		}
	}

	if v, ok := firstString(raw, "message", "msg"); ok {
		e.Message = v
	}

	if v, ok := firstString(raw, "timestamp", "time", "ts", "@timestamp"); ok {
		e.Timestamp = parseTime(v)
	}

	return e, nil
}

func parseTime(v string) time.Time {
	for _, layout := range commonTimeLayouts {
		if t, err := time.Parse(layout, v); err == nil {
			return t
		}
	}
	// Unix epoch seconds/millis as a numeric string.
	if n, err := strconv.ParseInt(v, 10, 64); err == nil {
		if n > 1e12 { // milliseconds
			return time.UnixMilli(n)
		}
		return time.Unix(n, 0)
	}
	return time.Time{}
}

func firstString(m map[string]interface{}, keys ...string) (string, bool) {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok {
				return s, true
			}
		}
	}
	return "", false
}

func firstNumber(m map[string]interface{}, keys ...string) (float64, bool) {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if n, ok := v.(float64); ok {
				return n, true
			}
			if s, ok := v.(string); ok {
				if n, err := strconv.ParseFloat(s, 64); err == nil {
					return n, true
				}
			}
		}
	}
	return 0, false
}
