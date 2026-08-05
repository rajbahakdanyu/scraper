package netutil

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPolicyRejectsPrivateTargets(t *testing.T) {
	policy := NewPolicy()
	for _, rawURL := range []string{
		"http://127.0.0.1/",
		"http://[::1]/",
		"http://169.254.169.254/latest/meta-data/",
		"http://localhost/",
		"file:///etc/passwd",
	} {
		if err := policy.ValidateURL(rawURL); err == nil {
			t.Errorf("ValidateURL accepted %s", rawURL)
		}
	}
}

func TestPolicyAllowsExplicitTestHost(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer ts.Close()

	client, err := NewPublicClient(time.Second, ts.URL)
	if err != nil {
		t.Fatalf("NewPublicClient failed: %v", err)
	}
	resp, err := client.Get(ts.URL)
	if err != nil {
		t.Fatalf("explicitly allowed test host failed: %v", err)
	}
	resp.Body.Close()
}

func TestPublicClientBlocksPrivateRedirect(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer target.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redirector.Close()

	client, err := NewPublicClient(time.Second, redirector.URL)
	if err != nil {
		t.Fatalf("NewPublicClient failed: %v", err)
	}
	if _, err := client.Get(redirector.URL); err == nil {
		t.Fatal("private redirect was not blocked")
	}
}

func TestWithUserAgentAddsConfiguredHeader(t *testing.T) {
	var got string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("User-Agent")
	}))
	defer ts.Close()

	client, err := NewPublicClient(time.Second, ts.URL)
	if err != nil {
		t.Fatalf("NewPublicClient failed: %v", err)
	}
	client = WithUserAgent(client, "Custom/1.0")
	resp, err := client.Get(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got != "Custom/1.0" {
		t.Fatalf("User-Agent = %q", got)
	}
}

func TestStreamingClientTimesOutStalledBodyRead(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
		case <-time.After(time.Second):
		}
	}))
	defer ts.Close()

	client, err := NewPublicStreamingClient(50*time.Millisecond, ts.URL)
	if err != nil {
		t.Fatalf("NewPublicStreamingClient failed: %v", err)
	}
	resp, err := client.Get(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	start := time.Now()
	if _, err := io.ReadAll(resp.Body); err == nil {
		t.Fatal("expected stalled body read to time out")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("idle timeout took %v", elapsed)
	}
}

func TestNewPublicClientWithProxySetsTransportProxy(t *testing.T) {
	proxyURL := "http://proxy.example.com:8080"
	client, err := NewPublicClientWithProxy(30*time.Second, proxyURL)
	if err != nil {
		t.Fatalf("NewPublicClientWithProxy failed: %v", err)
	}
	transport := client.Transport.(*http.Transport)
	req, _ := http.NewRequest("GET", "http://example.com", nil)
	proxy, err := transport.Proxy(req)
	if err != nil {
		t.Fatal(err)
	}
	if proxy == nil {
		t.Fatal("expected proxy to be set")
	}
	if proxy.Host != "proxy.example.com:8080" {
		t.Errorf("proxy host = %q, want proxy.example.com:8080", proxy.Host)
	}
}

func TestNewPublicClientWithProxyEmptyURL(t *testing.T) {
	client, err := NewPublicClientWithProxy(30*time.Second, "")
	if err != nil {
		t.Fatalf("NewPublicClientWithProxy failed: %v", err)
	}
	transport := client.Transport.(*http.Transport)
	if transport.Proxy != nil {
		t.Error("expected no proxy for empty URL")
	}
}

func TestNewPublicClientWithProxyInvalidURLFailsFast(t *testing.T) {
	_, err := NewPublicClientWithProxy(30*time.Second, "://invalid proxy url")
	if err == nil {
		t.Fatal("expected error for invalid proxy URL")
	}
}
