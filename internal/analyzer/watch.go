package analyzer

import (
	"bytes"
	"context"
	"io"
	"os"
	"time"
)

// WatchFile polls path for growth (like `tail -f`) and invokes onLine for
// every new line appended since the last poll. It's implemented with a
// simple stat+seek+read loop instead of fsnotify so the whole project
// stays free of external dependencies — for a CLI log tool, a 500ms
// poll interval is imperceptible and far simpler to reason about than
// wiring up inotify/kqueue.
//
// It also handles log rotation: if the file shrinks (size < last known
// offset) or its inode-ish identity changes via truncate, we reopen from
// the start.
func WatchFile(ctx context.Context, path string, interval time.Duration, onLine func([]byte)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	// Start at the end of the file — watch mode is about *new* activity,
	// mirroring `tail -f` (not `tail -f -n +1`).
	offset, err := f.Seek(0, os.SEEK_END)
	if err != nil {
		return err
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// pending holds bytes read past the last complete newline — a line
	// that's still being written to when we poll. We must NOT let the
	// file's read offset advance past what we've actually handed to
	// onLine, or a partial line would be silently dropped on the next
	// tick. Reading into our own buffer (instead of bufio.Reader, whose
	// internal read-ahead can pull the underlying fd's offset past what
	// ReadLine has returned) keeps `offset` exactly in sync with
	// "bytes fully processed".
	var pending []byte
	readBuf := make([]byte, 64*1024)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			info, err := f.Stat()
			if err != nil {
				return err
			}

			if info.Size() < offset {
				// File was truncated or rotated; start over.
				offset = 0
				pending = pending[:0]
				if _, err := f.Seek(0, os.SEEK_SET); err != nil {
					return err
				}
			}

			if info.Size() == offset {
				continue // nothing new
			}

			for {
				n, err := f.Read(readBuf)
				if n > 0 {
					pending = append(pending, readBuf[:n]...)
					offset += int64(n)

					for {
						i := bytes.IndexByte(pending, '\n')
						if i < 0 {
							break
						}
						line := pending[:i]
						if len(line) > 0 {
							onLine(bytes.TrimRight(line, "\r"))
						}
						pending = pending[i+1:]
					}
				}
				if err != nil {
					if err != io.EOF {
						return err
					}
					break
				}
			}
		}
	}
}
