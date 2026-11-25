// Package ping sends events to CronSpy. Network errors, 429 and 5xx are retried with backoff
// and jitter, honoring Retry-After; other 4xx (a bad key, an unknown monitor) are final.
package ping

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Event is one ping: its kind (start, success or fail) and its body.
type Event struct {
	Slug string `json:"slug"`
	Kind string `json:"kind"`
	Body Body   `json:"body"`
}

// Body is what a ping carries; empty fields are left out.
type Body struct {
	RunID      string             `json:"run_id,omitempty"`
	TS         time.Time          `json:"ts"`
	DurationMs *int64             `json:"duration_ms,omitempty"`
	ExitCode   *int               `json:"exit_code,omitempty"`
	Metrics    map[string]float64 `json:"metrics,omitempty"`
	LogTail    string             `json:"log_tail,omitempty"`
}

// Rejected is a final answer: retrying the same ping would get the same one.
type Rejected struct {
	Status  int
	Code    string
	Message string
}

func (r *Rejected) Error() string {
	if r.Message != "" {
		return fmt.Sprintf("%d %s: %s", r.Status, r.Code, r.Message)
	}
	return fmt.Sprintf("HTTP %d", r.Status)
}

// Client sends pings.
type Client struct {
	Endpoint  string
	Key       string
	UserAgent string
	// Timeout bounds each attempt.
	Timeout time.Duration
	// FirstDelay and MaxDelay shape the backoff: FirstDelay, twice that, … up to MaxDelay.
	FirstDelay, MaxDelay time.Duration
	HTTP                 *http.Client
}

// Send delivers e, retrying until ctx ends. It returns nil once delivered, a *Rejected for a
// final answer, or the last error when ctx ended first.
func (c *Client) Send(ctx context.Context, e Event) error {
	body, err := json.Marshal(e.Body)
	if err != nil {
		return err
	}
	target := c.Endpoint + "/v1/" + url.PathEscape(e.Slug)
	if e.Kind != "" {
		target += "/" + e.Kind
	}
	delay := c.FirstDelay
	for {
		wait, err := c.attempt(ctx, target, body)
		if err == nil {
			return nil
		}
		var rejected *Rejected
		if errors.As(err, &rejected) {
			return err
		}
		if wait == 0 {
			// Full jitter: a fleet of crons failing at once does not retry in step.
			wait = delay/2 + rand.N(delay/2+1) //nolint:gosec // jitter, not security
			delay = min(delay*2, c.MaxDelay)
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(wait):
		}
	}
}

// attempt makes one request; a positive wait is the server's Retry-After.
func (c *Client) attempt(ctx context.Context, target string, body []byte) (time.Duration, error) {
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return 0, &Rejected{Message: err.Error()}
	}
	req.Header.Set("Authorization", "Bearer "+c.Key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", c.UserAgent)
	res, err := c.HTTP.Do(req)
	if err != nil {
		return 0, err
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 16<<10))
	switch {
	case res.StatusCode < 300:
		return 0, nil
	case res.StatusCode == http.StatusTooManyRequests:
		return retryAfter(res.Header.Get("Retry-After")), fmt.Errorf("HTTP 429")
	case res.StatusCode >= 500 || res.StatusCode == http.StatusRequestTimeout:
		return 0, fmt.Errorf("HTTP %d", res.StatusCode)
	}
	var e struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	_ = json.Unmarshal(raw, &e)
	return 0, &Rejected{Status: res.StatusCode, Code: e.Error.Code, Message: e.Error.Message}
}

func retryAfter(v string) time.Duration {
	if s, err := strconv.Atoi(v); err == nil && s > 0 {
		return time.Duration(min(s, 300)) * time.Second
	}
	return 0
}
