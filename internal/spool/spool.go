// Package spool keeps the events that could not be sent, to send them on the next run. They
// arrive late: they count for the history, but no longer alert. The key is never written to
// disk, only a fingerprint of it, so an event goes out only with the key it was meant for.
package spool

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/cronspy/cronspy/internal/ping"
)

// MaxEvents bounds the spool; the oldest go first.
const MaxEvents = 500

// Entry is one event waiting to be sent.
type Entry struct {
	Endpoint string     `json:"endpoint"`
	Key      string     `json:"key_fingerprint"`
	Event    ping.Event `json:"event"`
	path     string
}

// Spool is a directory of pending events, one file each.
type Spool struct {
	Dir string
}

// Default is ~/.cronspy/spool, or a per-user folder in /var/tmp when there is no home.
func Default() Spool {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return Spool{Dir: filepath.Join(home, ".cronspy", "spool")}
	}
	return Spool{Dir: filepath.Join(os.TempDir(), fmt.Sprintf("cronspy-spool-%d", os.Getuid()))}
}

// Fingerprint identifies a key without storing it.
func Fingerprint(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:8])
}

// Add keeps an event for later.
func (s Spool) Add(endpoint, key string, e ping.Event) error {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(Entry{Endpoint: endpoint, Key: Fingerprint(key), Event: e})
	if err != nil {
		return err
	}
	name := fmt.Sprintf("%020d-%s-%s.json", time.Now().UnixNano(), e.Body.RunID, e.Kind)
	if err := os.WriteFile(filepath.Join(s.Dir, name), raw, 0o600); err != nil {
		return err
	}
	return s.trim()
}

// Pending lists the events for this endpoint and key, oldest first.
func (s Spool) Pending(endpoint, key string) ([]Entry, error) {
	names, err := s.names()
	if err != nil {
		return nil, err
	}
	fp := Fingerprint(key)
	var out []Entry
	for _, n := range names {
		p := filepath.Join(s.Dir, n)
		raw, err := os.ReadFile(p) //nolint:gosec // our own spool folder
		if err != nil {
			continue
		}
		var e Entry
		if json.Unmarshal(raw, &e) != nil {
			_ = os.Remove(p) // unreadable: it would never go out
			continue
		}
		if e.Endpoint == endpoint && e.Key == fp {
			e.path = p
			out = append(out, e)
		}
	}
	return out, nil
}

// Done removes a sent event.
func (s Spool) Done(e Entry) error {
	return os.Remove(e.path)
}

func (s Spool) names() ([]string, error) {
	entries, err := os.ReadDir(s.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	slices.Sort(names) // the names start with the time
	return names, nil
}

func (s Spool) trim() error {
	names, err := s.names()
	if err != nil {
		return err
	}
	for _, n := range names[:max(len(names)-MaxEvents, 0)] {
		_ = os.Remove(filepath.Join(s.Dir, n))
	}
	return nil
}
