// Package check performs a single HTTP health check.
package check

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// Target is what to check and how.
type Target struct {
	URL            string
	Method         string        // GET or HEAD
	Timeout        time.Duration // 0 = only the client's own timeout applies
	ExpectedStatus int           // 0 = any 2xx or 3xx counts as up
}

type Result struct {
	URL        string
	Up         bool
	StatusCode int
	Latency    time.Duration
	Err        error
	CheckedAt  time.Time
}

func Check(ctx context.Context, client *http.Client, t Target) Result {
	res := Result{URL: t.URL, CheckedAt: time.Now()}

	if t.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, t.Timeout)
		defer cancel()
	}

	req, err := http.NewRequestWithContext(ctx, t.Method, t.URL, nil)
	if err != nil {
		res.Err = err
		return res
	}

	start := time.Now()
	resp, err := client.Do(req)
	res.Latency = time.Since(start)
	if err != nil {
		res.Err = err
		return res
	}
	defer resp.Body.Close()

	res.StatusCode = resp.StatusCode
	if t.ExpectedStatus != 0 {
		res.Up = resp.StatusCode == t.ExpectedStatus
		if !res.Up {
			res.Err = fmt.Errorf("expected status %d, got %d", t.ExpectedStatus, resp.StatusCode)
		}
	} else {
		res.Up = resp.StatusCode >= 200 && resp.StatusCode < 400
	}
	return res
}
