package analyzer

import (
	"bufio"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"strings"
)

// OpenSource resolves a path into a readable stream, transparently
// decompressing gzip either because the extension is .gz or because the
// caller passed forceGzip=true. path == "-" reads from stdin.
//
// The caller is responsible for closing the returned io.ReadCloser.
func OpenSource(path string, forceGzip bool) (io.ReadCloser, error) {
	if path == "" || path == "-" {
		return io.NopCloser(os.Stdin), nil
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}

	if forceGzip || strings.HasSuffix(path, ".gz") {
		gz, err := gzip.NewReader(f)
		if err != nil {
			f.Close()
			return nil, fmt.Errorf("reading gzip header of %s: %w", path, err)
		}
		return &gzipReadCloser{gz: gz, f: f}, nil
	}

	return f, nil
}

type gzipReadCloser struct {
	gz *gzip.Reader
	f  *os.File
}

func (g *gzipReadCloser) Read(p []byte) (int, error) { return g.gz.Read(p) }
func (g *gzipReadCloser) Close() error {
	_ = g.gz.Close()
	return g.f.Close()
}

// LineReader streams lines of arbitrary length from r without the
// ~64KB ceiling that bufio.Scanner imposes by default. Log lines with
// giant embedded stack traces or payloads are common enough in the wild
// that this matters for a "huge file" tool.
type LineReader struct {
	br *bufio.Reader
}

func NewLineReader(r io.Reader) *LineReader {
	return &LineReader{br: bufio.NewReaderSize(r, 64*1024)}
}

// ReadLine returns the next line (without the trailing newline) or
// io.EOF when the stream is exhausted.
func (lr *LineReader) ReadLine() ([]byte, error) {
	var buf []byte
	for {
		chunk, isPrefix, err := lr.br.ReadLine()
		if len(chunk) > 0 {
			buf = append(buf, chunk...)
		}
		if err != nil {
			if len(buf) > 0 {
				return buf, nil
			}
			return nil, err
		}
		if !isPrefix {
			return buf, nil
		}
		// isPrefix == true means the line continues; loop to keep reading.
	}
}
