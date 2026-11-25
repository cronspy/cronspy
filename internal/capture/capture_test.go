package capture

import (
	"strings"
	"testing"
)

func TestTailKeepsTheEnd(t *testing.T) {
	tail := NewTail(10)
	_, _ = tail.Write([]byte("0123456789"))
	_, _ = tail.Write([]byte("abc"))
	if got := tail.String(); got != "3456789abc" {
		t.Fatalf("tail = %q", got)
	}
	// Cutting never leaves half a character.
	tail = NewTail(5)
	_, _ = tail.Write([]byte("xxxxñññ"))
	if got := tail.String(); !strings.HasPrefix(got, "ñ") || got != "ññ" {
		t.Fatalf("tail = %q", got)
	}
}

func TestMetricLines(t *testing.T) {
	tail := NewTail(1 << 10)
	_, _ = tail.Write([]byte("imported\nCRONSPY rows=12"))
	_, _ = tail.Write([]byte("04 files=3 bad=x we?ird=1 =4\nCRONSPY rows=1300\nnot CRONSPY x=1\nCRONSPY bytes=2.5e6"))
	tail.Flush()
	want := Metrics{"rows": 1300, "files": 3, "bytes": 2.5e6}
	if len(tail.Metrics) != len(want) {
		t.Fatalf("metrics = %v", tail.Metrics)
	}
	for k, v := range want {
		if tail.Metrics[k] != v {
			t.Fatalf("metrics = %v, want %v", tail.Metrics, want)
		}
	}
}
