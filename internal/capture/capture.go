// Package capture keeps what the job writes: the end of its output, for the log, and the
// metrics it reports in lines like "CRONSPY rows=1204 files=3".
package capture

import (
	"bytes"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

// Tail keeps the last n bytes written to it, from stdout and stderr in the order they arrive.
type Tail struct {
	mu   sync.Mutex
	max  int
	buf  []byte
	cut  bool
	line []byte
	// Metrics collects the CRONSPY lines seen so far.
	Metrics Metrics
}

// NewTail keeps up to max bytes.
func NewTail(max int) *Tail {
	return &Tail{max: max, Metrics: Metrics{}}
}

// Write records p; it never fails, so the job's output is never held back.
func (t *Tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if over := len(t.buf) - t.max; over > 0 {
		t.buf = append(t.buf[:0], t.buf[over:]...)
		t.cut = true
	}
	// Metric lines are read whole, however the writes split them.
	t.line = append(t.line, p...)
	for {
		i := bytes.IndexByte(t.line, '\n')
		if i < 0 {
			break
		}
		t.Metrics.Parse(string(t.line[:i]))
		t.line = t.line[i+1:]
	}
	if len(t.line) > 4096 {
		t.line = t.line[:0]
	}
	return len(p), nil
}

// String is the kept output, starting on a whole character.
func (t *Tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	b := t.buf
	if t.cut {
		for len(b) > 0 && !utf8.RuneStart(b[0]) {
			b = b[1:]
		}
	}
	return string(b)
}

// Flush reads a last metric line that had no newline.
func (t *Tail) Flush() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.line) > 0 {
		t.Metrics.Parse(string(t.line))
		t.line = t.line[:0]
	}
}

// Metrics are named numbers; the last value of a name wins.
type Metrics map[string]float64

// Parse reads a line "CRONSPY name=value …", or a bare "name=value …" from `cronspy metric`.
// Anything that is not a number is skipped, so a stray line never fails the run.
func (m Metrics) Parse(line string) {
	line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
	rest, ok := strings.CutPrefix(line, "CRONSPY ")
	if !ok {
		return
	}
	m.ParsePairs(rest)
}

// ParsePairs reads "name=value" pairs separated by spaces.
func (m Metrics) ParsePairs(s string) {
	for field := range strings.FieldsSeq(s) {
		name, value, ok := strings.Cut(field, "=")
		if !ok || !validName(name) {
			continue
		}
		if v, err := strconv.ParseFloat(value, 64); err == nil {
			m[name] = v
		}
	}
}

func validName(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.') {
			return false
		}
	}
	return true
}
