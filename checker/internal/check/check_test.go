package check

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func server(status int, delay time.Duration) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(delay)
		w.WriteHeader(status)
	}))
}

func TestCheck_OK(t *testing.T) {
	srv := server(http.StatusOK, 0)
	defer srv.Close()

	r := Check(context.Background(), srv.Client(), srv.URL)
	if !r.Up() || r.StatusCode != 200 {
		t.Fatalf("want up with 200, got %+v", r)
	}
}

func TestCheck_ServerError(t *testing.T) {
	srv := server(http.StatusInternalServerError, 0)
	defer srv.Close()

	r := Check(context.Background(), srv.Client(), srv.URL)
	if r.Up() || r.StatusCode != 500 || r.Err != nil {
		t.Fatalf("want down with 500 and no error, got %+v", r)
	}
}

func TestCheck_Timeout(t *testing.T) {
	srv := server(http.StatusOK, 200*time.Millisecond)
	defer srv.Close()

	client := &http.Client{Timeout: 50 * time.Millisecond}
	r := Check(context.Background(), client, srv.URL)
	if r.Up() || r.Err == nil {
		t.Fatalf("want timeout error, got %+v", r)
	}
}

func TestCheck_BadURL(t *testing.T) {
	r := Check(context.Background(), http.DefaultClient, "://bad")
	if r.Up() || r.Err == nil {
		t.Fatalf("want error for bad URL, got %+v", r)
	}
}
