package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/cronspy/cronspy/internal/spool"
)

// server records the pings it gets and answers status.
type server struct {
	*httptest.Server
	mu     sync.Mutex
	got    []recorded
	status int
}

type recorded struct {
	path string
	body map[string]any
}

func newServer(t *testing.T, status int) *server {
	s := &server{status: status}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		s.mu.Lock()
		s.got = append(s.got, recorded{r.URL.Path, body})
		status := s.status
		s.mu.Unlock()
		w.WriteHeader(status)
		if status == http.StatusNotFound {
			_, _ = w.Write([]byte(`{"error":{"code":"monitor_not_found","message":"Monitor 'nope' not found."}}`))
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *server) pings() []recorded {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]recorded(nil), s.got...)
}

// end is the success or fail ping, which the wrapper sends last.
func (s *server) end(t *testing.T) recorded {
	t.Helper()
	for _, p := range s.pings() {
		if !strings.HasSuffix(p.path, "/start") {
			return p
		}
	}
	t.Fatalf("no end ping among %v", s.pings())
	return recorded{}
}

type result struct {
	code           int
	stdout, stderr string
}

func runWith(t *testing.T, vars map[string]string, sp spool.Spool, signals chan os.Signal, args ...string) result {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := Main(args, Env{
		Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr,
		Getenv: func(k string) string { return vars[k] }, Signals: signals,
		Spool: sp, HTTP: http.DefaultClient, FirstDelay: time.Millisecond, Version: "test",
	})
	return result{code, stdout.String(), stderr.String()}
}

func TestReportsAFailedRun(t *testing.T) {
	srv := newServer(t, http.StatusAccepted)
	vars := map[string]string{"CRONSPY_KEY": "cs_ping_x", "CRONSPY_ENDPOINT": srv.URL}
	script := `echo importing; echo "CRONSPY rows=12 files=3"; echo "rows=40" >> "$CRONSPY_METRICS_FILE"; echo "connect with password=hunter22" >&2; exit 3`
	r := runWith(t, vars, spool.Spool{Dir: t.TempDir()}, nil, "run", "--monitor", "backup", "--", "sh", "-c", script)

	if r.code != 3 {
		t.Fatalf("exit %d, want the job's 3", r.code)
	}
	// The job's output passes through untouched.
	if !strings.Contains(r.stdout, "importing\nCRONSPY rows=12 files=3\n") || !strings.Contains(r.stderr, "password=hunter22") {
		t.Fatalf("stdout %q stderr %q", r.stdout, r.stderr)
	}
	end := srv.end(t)
	b := end.body
	metrics := b["metrics"].(map[string]any)
	if end.path != "/v1/backup/fail" || b["exit_code"] != float64(3) || metrics["rows"] != float64(40) || metrics["files"] != float64(3) {
		t.Fatalf("end = %s %v", end.path, b)
	}
	if log := b["log_tail"].(string); strings.Contains(log, "hunter22") || !strings.Contains(log, "password=[REDACTED]") {
		t.Fatalf("log_tail = %q", log)
	}
	if b["run_id"] == "" || b["duration_ms"] == nil {
		t.Fatalf("end = %v", b)
	}
	deadline := time.Now().Add(2 * time.Second)
	for len(srv.pings()) < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	for _, p := range srv.pings() {
		if p.body["run_id"] != b["run_id"] {
			t.Fatalf("start and end with different run ids: %v", srv.pings())
		}
	}
}

func TestSuccessAndNoLog(t *testing.T) {
	srv := newServer(t, http.StatusAccepted)
	vars := map[string]string{"CRONSPY_KEY": "cs_ping_x", "CRONSPY_ENDPOINT": srv.URL}
	r := runWith(t, vars, spool.Spool{Dir: t.TempDir()}, nil, "run", "--monitor", "backup", "--no-log", "--", "echo", "secret output")
	end := srv.end(t)
	if r.code != 0 || end.path != "/v1/backup/success" || end.body["log_tail"] != nil {
		t.Fatalf("code %d, end %s %v", r.code, end.path, end.body)
	}
}

func TestTheJobRunsWhateverHappens(t *testing.T) {
	// No key: the job runs unmonitored and keeps its exit code.
	r := runWith(t, map[string]string{}, spool.Spool{Dir: t.TempDir()}, nil, "run", "--monitor", "backup", "--", "sh", "-c", "echo ran; exit 4")
	if r.code != 4 || r.stdout != "ran\n" || !strings.Contains(r.stderr, "without monitoring") {
		t.Fatalf("%+v", r)
	}
	// A command that does not exist: 127, like a shell.
	srv := newServer(t, http.StatusAccepted)
	vars := map[string]string{"CRONSPY_KEY": "cs_ping_x", "CRONSPY_ENDPOINT": srv.URL}
	r = runWith(t, vars, spool.Spool{Dir: t.TempDir()}, nil, "run", "--monitor", "backup", "--", "no-such-command-xyz")
	if r.code != 127 || srv.end(t).path != "/v1/backup/fail" {
		t.Fatalf("missing command: %+v", r)
	}
}

func TestUnreachableRunIsSpooledAndSentNextTime(t *testing.T) {
	srv := newServer(t, http.StatusServiceUnavailable)
	vars := map[string]string{"CRONSPY_KEY": "cs_ping_x", "CRONSPY_ENDPOINT": srv.URL}
	sp := spool.Spool{Dir: t.TempDir()}
	r := runWith(t, vars, sp, nil, "run", "--monitor", "backup", "--retry-max-time", "50ms", "--", "true")
	if r.code != 0 || !strings.Contains(r.stderr, "saved") {
		t.Fatalf("%+v", r)
	}
	if pending, _ := sp.Pending(srv.URL, "cs_ping_x"); len(pending) != 1 {
		t.Fatalf("spooled %d", len(pending))
	}

	srv.mu.Lock()
	srv.status, srv.got = http.StatusAccepted, nil
	srv.mu.Unlock()
	runWith(t, vars, sp, nil, "run", "--monitor", "backup", "--", "sh", "-c", "sleep 0.3")
	if pending, _ := sp.Pending(srv.URL, "cs_ping_x"); len(pending) != 0 {
		t.Fatalf("%d still spooled", len(pending))
	}
	ends := 0
	for _, p := range srv.pings() {
		if p.path == "/v1/backup/success" {
			ends++
		}
	}
	if ends != 2 {
		t.Fatalf("got %v, want the spooled end and the new one", srv.pings())
	}
}

func TestRejectedRunIsNotSpooled(t *testing.T) {
	srv := newServer(t, http.StatusNotFound)
	vars := map[string]string{"CRONSPY_KEY": "cs_ping_x", "CRONSPY_ENDPOINT": srv.URL}
	sp := spool.Spool{Dir: t.TempDir()}
	r := runWith(t, vars, sp, nil, "run", "--monitor", "nope", "--", "true")
	if r.code != 0 || !strings.Contains(r.stderr, "monitor_not_found") {
		t.Fatalf("%+v", r)
	}
	if pending, _ := sp.Pending(srv.URL, "cs_ping_x"); len(pending) != 0 {
		t.Fatal("a rejected run was spooled")
	}
}

func TestSignalsReachTheJob(t *testing.T) {
	srv := newServer(t, http.StatusAccepted)
	vars := map[string]string{"CRONSPY_KEY": "cs_ping_x", "CRONSPY_ENDPOINT": srv.URL}
	signals := make(chan os.Signal, 1)
	go func() {
		time.Sleep(300 * time.Millisecond)
		signals <- syscall.SIGTERM
	}()
	r := runWith(t, vars, spool.Spool{Dir: t.TempDir()}, signals, "run", "--monitor", "backup", "--", "sh", "-c", "trap 'echo stopping; exit 42' TERM; sleep 5 & wait")
	if r.code != 42 || !strings.Contains(r.stdout, "stopping") || srv.end(t).body["exit_code"] != float64(42) {
		t.Fatalf("%+v, end %v", r, srv.end(t).body)
	}
}

func TestDryRunSendsNothing(t *testing.T) {
	srv := newServer(t, http.StatusAccepted)
	vars := map[string]string{"CRONSPY_ENDPOINT": srv.URL}
	r := runWith(t, vars, spool.Spool{Dir: t.TempDir()}, nil, "run", "--monitor", "backup", "--dry-run", "--", "sh", "-c", "echo token=abc123; exit 1")
	if r.code != 1 || len(srv.pings()) != 0 {
		t.Fatalf("code %d, sent %v", r.code, srv.pings())
	}
	if !strings.Contains(r.stderr, "would POST "+srv.URL+"/v1/backup/start") || !strings.Contains(r.stderr, "/v1/backup/fail") || !strings.Contains(r.stderr, "token=[REDACTED]") {
		t.Fatalf("stderr = %s", r.stderr)
	}
}

func TestMetricOutsideARun(t *testing.T) {
	r := runWith(t, map[string]string{}, spool.Spool{}, nil, "metric", "rows=1")
	if r.code != 0 || !strings.Contains(r.stderr, "not inside") {
		t.Fatalf("%+v", r)
	}
	f, _ := os.CreateTemp(t.TempDir(), "m")
	_ = f.Close()
	runWith(t, map[string]string{metricsEnv: f.Name()}, spool.Spool{}, nil, "metric", "rows=1", "files=2")
	raw, _ := os.ReadFile(f.Name())
	if string(raw) != "rows=1 files=2\n" {
		t.Fatalf("file = %q", raw)
	}
}
