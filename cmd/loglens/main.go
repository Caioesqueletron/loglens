// Command loglens is a small, educational JSON log analyzer for backend
// applications. See README.md for architecture notes.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/example/loglens/internal/analyzer"
)

type statusFlags []int

func (s *statusFlags) String() string {
	strs := make([]string, len(*s))
	for i, v := range *s {
		strs[i] = strconv.Itoa(v)
	}
	return strings.Join(strs, ",")
}
func (s *statusFlags) Set(v string) error {
	for _, part := range strings.Split(v, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil {
			return fmt.Errorf("invalid --status value %q: %w", part, err)
		}
		*s = append(*s, n)
	}
	return nil
}

type fieldFlags []string

func (f *fieldFlags) String() string { return strings.Join(*f, ",") }
func (f *fieldFlags) Set(v string) error {
	*f = append(*f, v)
	return nil
}

func main() {
	var statuses statusFlags
	var fields fieldFlags

	flag.Var(&statuses, "status", "filter by HTTP status code, repeatable or comma-separated (e.g. --status 500,503)")
	flag.Var(&fields, "field", "filter by arbitrary field, repeatable (e.g. --field env=prod)")
	last := flag.String("last", "", "only consider entries from the last duration, e.g. 1h, 15m")
	groupBy := flag.String("group-by", "", "group counts by this raw JSON field, e.g. service")
	watch := flag.Bool("watch", false, "keep the process running and re-summarize as the file grows (like tail -f)")
	watchInterval := flag.Duration("watch-interval", 500*time.Millisecond, "poll interval for --watch")
	gzipFlag := flag.Bool("gzip", false, "force gzip decompression regardless of file extension")
	jsonOut := flag.Bool("json", false, "print the summary as JSON instead of the human-readable report")
	topErrors := flag.Int("top-errors", 5, "how many top error groups to show")
	flag.Parse()

	path := flag.Arg(0) // may be "" or "-" for stdin

	since, err := analyzer.ParseSince(*last)
	if err != nil {
		fatal(err)
	}
	fieldFilters, err := analyzer.ParseFieldFilters(fields)
	if err != nil {
		fatal(err)
	}

	statusSet := make(map[int]bool, len(statuses))
	for _, s := range statuses {
		statusSet[s] = true
	}

	filter := analyzer.Filter{Statuses: statusSet, Since: since, Fields: fieldFilters}

	if *watch {
		runWatch(path, *watchInterval, *gzipFlag, filter, *groupBy, *topErrors, *jsonOut)
		return
	}

	stats := analyzer.NewStats(*groupBy)
	if err := scanOnce(path, *gzipFlag, filter, stats); err != nil {
		fatal(err)
	}

	printSummary(stats.Summary(*topErrors), *jsonOut)
}

// scanOnce streams the whole file/stdin once, applying filter and feeding
// matching entries into stats. Uses analyzer.LineReader so a single
// absurdly long line (e.g. an embedded stack trace) never blows past a
// fixed buffer size like bufio.Scanner's default 64KB token limit.
func scanOnce(path string, forceGzip bool, filter analyzer.Filter, stats *analyzer.Stats) error {
	src, err := analyzer.OpenSource(path, forceGzip)
	if err != nil {
		return err
	}
	defer src.Close()

	lr := analyzer.NewLineReader(src)
	for {
		line, err := lr.ReadLine()
		if len(line) > 0 {
			entry, _ := analyzer.ParseLine(line) // malformed lines degrade to Message-only
			if filter.Match(entry) {
				stats.Add(entry)
			}
		}
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

func runWatch(path string, interval time.Duration, forceGzip bool, filter analyzer.Filter, groupBy string, topN int, jsonOut bool) {
	if path == "" || path == "-" {
		fatal(fmt.Errorf("--watch requires a real file path, not stdin"))
	}

	stats := analyzer.NewStats(groupBy)

	// Prime with whatever already exists in the file so --watch on a
	// freshly-started tool still shows current totals, then keep tailing.
	if err := scanOnce(path, forceGzip, filter, stats); err != nil {
		fatal(err)
	}
	printSummary(stats.Summary(topN), jsonOut)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	err := analyzer.WatchFile(ctx, path, interval, func(line []byte) {
		entry, _ := analyzer.ParseLine(line)
		if filter.Match(entry) {
			stats.Add(entry)
			clearScreen()
			printSummary(stats.Summary(topN), jsonOut)
		}
	})
	if err != nil && err != context.Canceled {
		fatal(err)
	}
}

func clearScreen() {
	fmt.Print("\033[H\033[2J")
}

func printSummary(s analyzer.Summary, jsonOut bool) {
	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(s)
		return
	}

	fmt.Printf("Total requests: %s\n", commas(s.Total))
	fmt.Printf("Errors:         %s\n", commas(s.Errors))
	fmt.Printf("Error rate:     %.2f%%\n\n", s.ErrorRate)
	fmt.Printf("p50: %s\n", fmtMs(s.P50Ms))
	fmt.Printf("p95: %s\n", fmtMs(s.P95Ms))
	fmt.Printf("p99: %s\n", fmtMs(s.P99Ms))

	if len(s.TopErrors) > 0 {
		fmt.Println("\nTop errors:")
		for _, e := range s.TopErrors {
			fmt.Printf("%d - %-30s %d\n", e.Status, e.Message, e.Count)
		}
	}

	if len(s.Groups) > 0 {
		fmt.Println("\nGroups:")
		for _, g := range s.Groups {
			fmt.Printf("%-30s %d\n", g.Value, g.Count)
		}
	}
}

func fmtMs(ms float64) string {
	if ms >= 1000 {
		return fmt.Sprintf("%.1fs", ms/1000)
	}
	return fmt.Sprintf("%.0fms", ms)
}

func commas(n int) string {
	s := strconv.Itoa(n)
	if len(s) <= 3 {
		return s
	}
	var out []byte
	rem := len(s) % 3
	if rem > 0 {
		out = append(out, s[:rem]...)
		if len(s) > rem {
			out = append(out, ',')
		}
	}
	for i := rem; i < len(s); i += 3 {
		out = append(out, s[i:i+3]...)
		if i+3 < len(s) {
			out = append(out, ',')
		}
	}
	return string(out)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
