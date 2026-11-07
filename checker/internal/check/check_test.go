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
		w.Header().Set("X-Method", r.Method)
		w.WriteHeader(status)
	}))
}

func TestCheck_OK(t *testing.T) {
	srv := server(http.StatusOK, 0)
	defer srv.Close()

	r := Check(context.Background(), srv.Client(), Target{URL: srv.URL})
	if !r.Up || r.StatusCode != 200 || r.Err != nil {
		t.Fatalf("want up with 200, got %+v", r)
	}
}

func TestCheck_ServerError(t *testing.T) {
	srv := server(http.StatusInternalServerError, 0)
	defer srv.Close()

	r := Check(context.Background(), srv.Client(), Target{URL: srv.URL})
	if r.Up || r.StatusCode != 500 || r.Err != nil {
		t.Fatalf("want down with 500 and no error, got %+v", r)
	}
}

func TestCheck_ExpectedStatus(t *testing.T) {
	srv := server(http.StatusNoContent, 0)
	defer srv.Close()

	if r := Check(context.Background(), srv.Client(), Target{URL: srv.URL, ExpectedStatus: 204}); !r.Up {
		t.Errorf("204 expected and returned: want up, got %+v", r)
	}
	r := Check(context.Background(), srv.Client(), Target{URL: srv.URL, ExpectedStatus: 200})
	if r.Up || r.Err == nil {
		t.Errorf("200 expected, 204 returned: want down with error, got %+v", r)
	}
}

func TestCheck_HEAD(t *testing.T) {
	var method string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
	}))
	defer srv.Close()

	if r := Check(context.Background(), srv.Client(), Target{URL: srv.URL, Method: http.MethodHead}); !r.Up || method != "HEAD" {
		t.Fatalf("want up via HEAD, got %+v (method %q)", r, method)
	}
}

func TestCheck_Timeout(t *testing.T) {
	srv := server(http.StatusOK, 200*time.Millisecond)
	defer srv.Close()

	r := Check(context.Background(), srv.Client(), Target{URL: srv.URL, Timeout: 50 * time.Millisecond})
	if r.Up || r.Err == nil {
		t.Fatalf("want timeout error, got %+v", r)
	}
}

func TestCheck_BadURL(t *testing.T) {
	r := Check(context.Background(), http.DefaultClient, Target{URL: "://bad"})
	if r.Up || r.Err == nil {
		t.Fatalf("want error for bad URL, got %+v", r)
	}
}
