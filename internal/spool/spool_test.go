package spool

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cronspy/cronspy/internal/ping"
)

func TestSpool(t *testing.T) {
	s := Spool{Dir: filepath.Join(t.TempDir(), "spool")}
	if p, err := s.Pending("https://ping", "cs_ping_a"); err != nil || len(p) != 0 {
		t.Fatalf("empty spool: %v %v", p, err)
	}
	for i, key := range []string{"cs_ping_a", "cs_ping_b", "cs_ping_a"} {
		if err := s.Add("https://ping", key, ping.Event{Slug: "backup", Kind: "success", Body: ping.Body{RunID: fmt.Sprint(i)}}); err != nil {
			t.Fatal(err)
		}
	}
	pending, _ := s.Pending("https://ping", "cs_ping_a")
	if len(pending) != 2 || pending[0].Event.Body.RunID != "0" || pending[1].Event.Body.RunID != "2" {
		t.Fatalf("pending = %+v", pending)
	}
	files, _ := os.ReadDir(s.Dir)
	for _, f := range files {
		raw, _ := os.ReadFile(filepath.Join(s.Dir, f.Name()))
		if strings.Contains(string(raw), "cs_ping_") {
			t.Fatalf("a key was written to disk: %s", raw)
		}
		if info, _ := f.Info(); info.Mode().Perm() != 0o600 {
			t.Fatalf("file mode %v", info.Mode().Perm())
		}
	}
	if err := s.Done(pending[0]); err != nil {
		t.Fatal(err)
	}
	if left, _ := s.Pending("https://ping", "cs_ping_a"); len(left) != 1 {
		t.Fatalf("after one sent: %+v", left)
	}
}

func TestSpoolIsBounded(t *testing.T) {
	s := Spool{Dir: t.TempDir()}
	for i := range MaxEvents + 5 {
		_ = s.Add("e", "k", ping.Event{Slug: "x", Body: ping.Body{RunID: fmt.Sprint(i)}})
	}
	pending, _ := s.Pending("e", "k")
	if len(pending) != MaxEvents || pending[0].Event.Body.RunID != "5" {
		t.Fatalf("%d pending, oldest %s", len(pending), pending[0].Event.Body.RunID)
	}
}
