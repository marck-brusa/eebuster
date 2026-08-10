// Package logbuf is a small bounded ring buffer of recent log lines, so the console output a
// terminal user already sees can also back the dashboard's Diagnostics log viewer -- there's
// no per-process supervisor with its own log files anymore (see docs/), just this one
// process's own stdout.
package logbuf

import (
	"strings"
	"sync"
)

type Buffer struct {
	mu    sync.Mutex
	lines []string
	max   int
}

func New(max int) *Buffer {
	return &Buffer{max: max}
}

// Write implements io.Writer so this can sit alongside os.Stdout in an io.MultiWriter passed
// to log.SetOutput -- every line the console prints also lands here.
func (b *Buffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, line := range strings.Split(strings.TrimRight(string(p), "\n"), "\n") {
		b.lines = append(b.lines, line)
	}
	if len(b.lines) > b.max {
		b.lines = b.lines[len(b.lines)-b.max:]
	}
	return len(p), nil
}

// Tail returns the last n lines (all of them if n <= 0 or n exceeds what's buffered).
func (b *Buffer) Tail(n int) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if n <= 0 || n > len(b.lines) {
		n = len(b.lines)
	}
	out := make([]string, n)
	copy(out, b.lines[len(b.lines)-n:])
	return out
}
