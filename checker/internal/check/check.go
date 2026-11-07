// Package check performs a single HTTP health check.
package check

import (
	"context"
	"net/http"
	"time"
)

type Result struct {
	URL        string
	StatusCode int
	Latency    time.Duration
	Err        error
	CheckedAt  time.Time
}

// Up reports whether the target answered with a non-error status.
func (r Result) Up() bool {
	return r.Err == nil && r.StatusCode >= 200 && r.StatusCode < 400
}

func Check(ctx context.Context, client *http.Client, url string) Result {
	res := Result{URL: url, CheckedAt: time.Now()}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
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
	return res
}
