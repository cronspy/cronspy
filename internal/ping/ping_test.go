package ping

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func client(url string) *Client {
	return &Client{Endpoint: url, Key: "cs_ping_x", UserAgent: "cronspy/test", Timeout: time.Second,
		FirstDelay: time.Millisecond, MaxDelay: 5 * time.Millisecond, HTTP: http.DefaultClient}
}

func TestRetriesUntilDelivered(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/backup/success" || r.Header.Get("Authorization") != "Bearer cs_ping_x" {
			t.Errorf("got %s with %q", r.URL.Path, r.Header.Get("Authorization"))
		}
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	if err := client(srv.URL).Send(t.Context(), Event{Slug: "backup", Kind: "success"}); err != nil || calls.Load() != 3 {
		t.Fatalf("err %v after %d calls", err, calls.Load())
	}
}

func TestFinalAnswersAreNotRetried(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"monitor_not_found","message":"Monitor 'x' not found."}}`))
	}))
	defer srv.Close()
	err := client(srv.URL).Send(t.Context(), Event{Slug: "x"})
	var rejected *Rejected
	if !errors.As(err, &rejected) || rejected.Code != "monitor_not_found" || calls.Load() != 1 {
		t.Fatalf("err %v after %d calls", err, calls.Load())
	}
}

func TestRetryAfter(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()
	start := time.Now()
	if err := client(srv.URL).Send(t.Context(), Event{Slug: "x"}); err != nil || time.Since(start) < time.Second {
		t.Fatalf("err %v after %v: Retry-After not honored", err, time.Since(start))
	}
}

func TestGivesUpWhenTheContextEnds(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	if err := client(srv.URL).Send(ctx, Event{Slug: "x"}); err == nil {
		t.Fatal("delivered against a server that is down")
	}
}
