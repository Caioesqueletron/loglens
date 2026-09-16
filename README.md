# loglens

A small, educational JSON log analyzer for backend applications. Point it
at (potentially huge) newline-delimited JSON log files and get the kind of
summary you'd otherwise cobble together from `jq` + `awk` + a spreadsheet.

## Quick start

```bash
go build -o loglens ./cmd/loglens

./loglens examples/app.log --status 500 --last 24h
```

```
Total requests: 10
Errors:         2
Error rate:     20.00%

p50: 45ms
p95: 1157ms
p99: 1194ms

Top errors:
500 - database timeout               2
```

Read from stdin (e.g. piped from `kubectl logs`):

```bash
kubectl logs deploy/api -f | ./loglens - --group-by service
```

## Features

| Feature                          | Flag(s)                          |
|-----------------------------------|-----------------------------------|
| JSON log parsing                  | automatic, tolerant of mixed schemas |
| Filter by HTTP status              | `--status 500,503` (repeatable/comma-separated) |
| Filter by time window              | `--last 1h`                       |
| Filter by arbitrary field          | `--field env=prod` (repeatable)   |
| Group counts by a field            | `--group-by service`              |
| Latency percentiles (p50/p95/p99)  | always computed                   |
| Top errors table                   | `--top-errors 5`                  |
| Streaming huge files                | built-in, no full-file buffering  |
| stdin support                      | pass `-` or omit the path          |
| gzip support                       | auto-detected `.gz`, or `--gzip`   |
| Watch mode (like `tail -f`)         | `--watch`                          |
| JSON output                        | `--json`                          |

## Architecture

```
cmd/loglens/main.go          CLI: flags, orchestration, rendering
internal/analyzer/
  entry.go                   Entry: normalized view over one JSON log line
  filter.go                  Filter: status / time-window / field matching
  source.go                  File/stdin opening + gzip + huge-line streaming
  stats.go                   Stats: percentiles, error grouping, field grouping
  watch.go                   Polling-based tail -f (no external fsnotify dep)
```

### Streaming, not loading

`internal/analyzer/source.go` defines `LineReader`, a thin wrapper around
`bufio.Reader.ReadLine` that reassembles lines of *any* length. Plain
`bufio.Scanner` has a default 64KB-per-token ceiling (`bufio.ErrTooLong`
if a line — say, one with an embedded stack trace — exceeds it); `loglens`
never buffers more than one line in memory at a time, so a multi-gigabyte
log file is analyzed in constant memory, one line at a time, regardless
of how it's structured.

### Tolerant parsing

`entry.go` doesn't assume a fixed schema. It looks for status under
several common key names (`status`, `status_code`, `statusCode`, `code`),
latency under several more (`latency_ms`, `duration_ms`, `latency`, ...),
and falls back gracefully — a line that isn't valid JSON at all is still
counted, just without status/latency data, instead of aborting the whole
run. This matters in practice: real log streams are rarely 100% clean.

### Watch mode without fsnotify

`watch.go` implements `tail -f` semantics with a plain `stat` + `read` +
`time.Ticker` polling loop instead of pulling in `fsnotify`. The subtlety
worth calling out: a naive implementation that wraps the file in a fresh
`bufio.Reader` on every poll will silently drop data, because
`bufio.Reader` read-aheads past what `ReadLine()` has actually returned,
advancing the underlying file descriptor's offset beyond the last
*complete* line. `WatchFile` instead reads raw bytes into its own buffer,
splits on `\n` itself, and only ever advances `offset` by exactly the
number of bytes read from the fd — so a line still being written
mid-poll is correctly held over (as `pending`) to the next tick instead
of being lost. It also detects truncation/rotation (`file size < last
known offset`) and reopens from the start.

### Grouping & top errors

`Stats.Add` buckets errors by `(status, truncated message)` so `500
database timeout` and `500 connection refused` are tracked separately
even though they share a status code — closer to what you actually want
when triaging. `--group-by service` does the same for an arbitrary field,
useful for "which service is generating all these errors" style
questions.

## What this demonstrates

- Constant-memory streaming over arbitrarily large files (no `ReadAll`, no scanner line-length ceiling)
- Correct polling-based file tailing (the offset/buffering subtlety above)
- Defensive parsing of heterogeneous, "real world" JSON logs
- `context.Context`-driven graceful shutdown of `--watch` on Ctrl+C
- Clean separation between parsing (`entry.go`), filtering (`filter.go`), and aggregation (`stats.go`)

## Limitations (by design, for an educational project)

- Percentiles sort all retained latency samples in memory; a production
  tool processing billions of lines would want a streaming histogram
  (e.g. t-digest or HDRHistogram) instead.
- Timestamp parsing covers common layouts (RFC3339, Unix epoch) but isn't
  exhaustive — add more to `commonTimeLayouts` in `entry.go` as needed.
