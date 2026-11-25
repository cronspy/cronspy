// Package cli implements the cronspy command.
package cli

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/cronspy/cronspy/internal/capture"
	"github.com/cronspy/cronspy/internal/ping"
	"github.com/cronspy/cronspy/internal/spool"
	"github.com/cronspy/cronspy/redact"
)

const (
	// DefaultEndpoint is CronSpy's ping API.
	DefaultEndpoint = "https://ping.cronspy.com"
	// MaxTail is the most log the API accepts in a ping.
	MaxTail = 64 << 10
	// metricsEnv names the file `cronspy metric` appends to while a job runs.
	metricsEnv = "CRONSPY_METRICS_FILE"
)

// Env is what the command reads and writes outside itself; tests replace it.
type Env struct {
	Stdin          io.Reader
	Stdout, Stderr io.Writer
	Getenv         func(string) string
	// Signals delivers the signals to forward to the job; nil listens to the process's own.
	Signals <-chan os.Signal
	Spool   spool.Spool
	HTTP    *http.Client
	// FirstDelay is the first retry backoff (1 s unless a test shortens it).
	FirstDelay time.Duration
	Version    string
}

// Main runs the command and returns the exit code.
func Main(args []string, env Env) int {
	if len(args) == 0 {
		usage(env.Stderr)
		return 2
	}
	switch args[0] {
	case "run":
		return run(args[1:], env)
	case "metric":
		return metric(args[1:], env)
	case "version", "--version":
		_, _ = fmt.Fprintln(env.Stdout, "cronspy", env.Version)
		return 0
	case "help", "-h", "--help":
		usage(env.Stdout)
		return 0
	}
	_, _ = fmt.Fprintf(env.Stderr, "cronspy: unknown command %q\n\n", args[0])
	usage(env.Stderr)
	return 2
}

func usage(w io.Writer) {
	_, _ = fmt.Fprint(w, `cronspy watches a cron job and tells CronSpy how it went.

Usage:
  cronspy run --monitor <slug> [flags] -- <command> [args…]
  cronspy metric name=value…    report metrics from inside a job run by cronspy run
  cronspy version

The job always runs and cronspy exits with its exit code, whatever happens to the pings.
Run "cronspy run --help" for the flags. Docs: https://cronspy.com/docs/integrations/cli/
`)
}

type options struct {
	monitor, key, endpoint string
	tailBytes              int
	timeout, retryMaxTime  time.Duration
	noLog, dryRun          bool
	command                []string
}

func parseRun(args []string, env Env) (options, error) {
	o := options{}
	fs := flag.NewFlagSet("cronspy run", flag.ContinueOnError)
	fs.SetOutput(env.Stderr)
	fs.StringVar(&o.monitor, "monitor", env.Getenv("CRONSPY_MONITOR"), "monitor slug (or CRONSPY_MONITOR)")
	fs.StringVar(&o.key, "key", "", "ping key, cs_ping_… (default: CRONSPY_KEY)")
	fs.StringVar(&o.endpoint, "endpoint", or(env.Getenv("CRONSPY_ENDPOINT"), DefaultEndpoint), "ping API")
	fs.IntVar(&o.tailBytes, "tail-bytes", MaxTail, "bytes of output kept for the log (at most 65536)")
	fs.DurationVar(&o.timeout, "timeout", 10*time.Second, "timeout of each request")
	fs.DurationVar(&o.retryMaxTime, "retry-max-time", 2*time.Minute, "how long to retry the end of the run before saving it for the next one")
	fs.BoolVar(&o.noLog, "no-log", false, "send no output, only the result, duration and metrics")
	fs.BoolVar(&o.dryRun, "dry-run", false, "run the job and print what would be sent, sending nothing")
	if err := fs.Parse(args); err != nil {
		return o, err
	}
	o.command = fs.Args()
	if len(o.command) == 0 {
		return o, errors.New("no command: cronspy run --monitor <slug> -- <command> [args…]")
	}
	if o.key == "" {
		o.key = env.Getenv("CRONSPY_KEY")
	}
	o.tailBytes = min(max(o.tailBytes, 0), MaxTail)
	o.endpoint = strings.TrimRight(o.endpoint, "/")
	return o, nil
}

func run(args []string, env Env) int {
	o, err := parseRun(args, env)
	if err != nil {
		if !errors.Is(err, flag.ErrHelp) {
			_, _ = fmt.Fprintln(env.Stderr, "cronspy:", err)
		}
		return 2
	}
	warn := func(format string, a ...any) { _, _ = fmt.Fprintf(env.Stderr, "cronspy: "+format+"\n", a...) }

	// Without a monitor or a key the job still runs: monitoring must never stop it.
	monitored := o.monitor != "" && (o.key != "" || o.dryRun)
	if !monitored {
		missing := "--monitor"
		if o.monitor != "" {
			missing = "a key (--key or CRONSPY_KEY)"
		}
		warn("no %s: running the job without monitoring", missing)
	}

	client := &ping.Client{
		Endpoint: o.endpoint, Key: o.key, UserAgent: "cronspy/" + env.Version, Timeout: o.timeout,
		FirstDelay: or(env.FirstDelay, time.Second), MaxDelay: 30 * time.Second, HTTP: env.HTTP,
	}
	runID := uuid()
	started := time.Now().UTC()
	background, stopBackground := context.WithCancel(context.Background())
	defer stopBackground()
	if monitored && !o.dryRun {
		go flushSpool(background, client, env.Spool)
		// The start is retried while the job runs and never delays it.
		go func() {
			_ = client.Send(background, ping.Event{Slug: o.monitor, Kind: "start", Body: ping.Body{RunID: runID, TS: started}})
		}()
	}
	if monitored && o.dryRun {
		show(env.Stderr, o.endpoint, ping.Event{Slug: o.monitor, Kind: "start", Body: ping.Body{RunID: runID, TS: started}})
	}

	tail := capture.NewTail(max(o.tailBytes, 1))
	metricsFile, cleanup := tempMetricsFile()
	defer cleanup()
	code := execute(o.command, env, tail, runID, metricsFile, warn)
	finished := time.Now().UTC()
	stopBackground()
	if !monitored {
		return code
	}

	tail.Flush()
	if metricsFile != "" {
		if raw, err := os.ReadFile(metricsFile); err == nil { //nolint:gosec // our own temp file
			for line := range strings.SplitSeq(string(raw), "\n") {
				tail.Metrics.ParsePairs(line)
			}
		}
	}
	duration := finished.Sub(started).Milliseconds()
	end := ping.Event{Slug: o.monitor, Kind: "success", Body: ping.Body{
		RunID: runID, TS: finished, DurationMs: &duration, ExitCode: &code,
	}}
	if code != 0 {
		end.Kind = "fail"
	}
	if len(tail.Metrics) > 0 {
		end.Body.Metrics = tail.Metrics
	}
	if !o.noLog {
		// Secrets are hidden here, before anything leaves the machine.
		end.Body.LogTail = redact.String(tail.String())
	}
	if o.dryRun {
		show(env.Stderr, o.endpoint, end)
		return code
	}

	ctx, cancel := context.WithTimeout(context.Background(), o.retryMaxTime)
	defer cancel()
	err = client.Send(ctx, end)
	var rejected *ping.Rejected
	switch {
	case errors.As(err, &rejected):
		warn("CronSpy did not take the run (%v); check --monitor and the key", rejected)
	case err != nil:
		if serr := env.Spool.Add(o.endpoint, o.key, end); serr != nil {
			warn("could not reach CronSpy (%v) nor save the run for later (%v)", err, serr)
		} else {
			warn("could not reach CronSpy (%v); the run is saved and goes out with the next one", err)
		}
	}
	return code
}

// execute runs the command with its output passing through untouched, forwards signals to it,
// and returns its exit code: 128+n when a signal ended it, 127 when it could not start.
func execute(command []string, env Env, tail *capture.Tail, runID, metricsFile string, warn func(string, ...any)) int {
	cmd := exec.Command(command[0], command[1:]...) //nolint:gosec // running the user's command is the point
	cmd.Stdin = env.Stdin
	cmd.Stdout = io.MultiWriter(env.Stdout, tail)
	cmd.Stderr = io.MultiWriter(env.Stderr, tail)
	cmd.Env = append(os.Environ(), "CRONSPY_RUN_ID="+runID)
	if metricsFile != "" {
		cmd.Env = append(cmd.Env, metricsEnv+"="+metricsFile)
	}
	ownGroup(cmd)
	if err := cmd.Start(); err != nil {
		warn("could not start %q: %v", command[0], err)
		_, _ = fmt.Fprintf(tail, "cronspy: could not start %q: %v\n", command[0], err)
		if errors.Is(err, os.ErrPermission) {
			return 126
		}
		return 127
	}

	signals := env.Signals
	if signals == nil {
		ch := make(chan os.Signal, 4)
		signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
		defer signal.Stop(ch)
		signals = ch
	}
	done := make(chan struct{})
	go func() {
		for {
			select {
			case s := <-signals:
				forward(cmd, s)
			case <-done:
				return
			}
		}
	}()
	err := cmd.Wait()
	close(done)

	var exit *exec.ExitError
	if errors.As(err, &exit) {
		if status, ok := exit.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			return 128 + int(status.Signal())
		}
		return exit.ExitCode()
	}
	if err != nil {
		warn("%v", err)
		return 1
	}
	return 0
}

// flushSpool sends the events saved by earlier runs, while the job runs.
func flushSpool(ctx context.Context, client *ping.Client, s spool.Spool) {
	pending, err := s.Pending(client.Endpoint, client.Key)
	if err != nil {
		return
	}
	for _, e := range pending {
		attempt, cancel := context.WithTimeout(ctx, client.Timeout)
		err := client.Send(attempt, e.Event)
		cancel()
		var rejected *ping.Rejected
		if err == nil || errors.As(err, &rejected) {
			_ = s.Done(e)
			continue
		}
		return // still unreachable: the rest waits for the next run
	}
}

// metric appends metrics for the run that started this process.
func metric(args []string, env Env) int {
	path := env.Getenv(metricsEnv)
	if path == "" {
		_, _ = fmt.Fprintln(env.Stderr, "cronspy metric: not inside `cronspy run`; nothing is sent")
		return 0
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // the path cronspy run gave us
	if err != nil {
		_, _ = fmt.Fprintln(env.Stderr, "cronspy metric:", err)
		return 0
	}
	defer func() { _ = f.Close() }()
	_, _ = fmt.Fprintln(f, strings.Join(args, " "))
	return 0
}

func tempMetricsFile() (string, func()) {
	f, err := os.CreateTemp("", "cronspy-metrics-*")
	if err != nil {
		return "", func() {}
	}
	_ = f.Close()
	return f.Name(), func() { _ = os.Remove(f.Name()) }
}

// show prints an event as it would be sent, for --dry-run.
func show(w io.Writer, endpoint string, e ping.Event) {
	raw, _ := json.MarshalIndent(e.Body, "", "  ")
	_, _ = fmt.Fprintf(w, "cronspy: would POST %s/v1/%s/%s\n%s\n", endpoint, e.Slug, e.Kind, raw)
}

// uuid is a random (version 4) UUID.
func uuid() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func or[T comparable](v, fallback T) T {
	var zero T
	if v == zero {
		return fallback
	}
	return v
}
