package netutil

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPStatusErrorRedactsURL(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/boom", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	var target map[string]interface{}
	err := GetJSON(context.Background(), ts.Client(), ts.URL+"/boom?token=SECRET", "ua", &target)
	var se *HTTPStatusError
	if !errors.As(err, &se) {
		t.Fatalf("expected HTTPStatusError, got %T", err)
	}
	if strings.Contains(se.Error(), "token=SECRET") {
		t.Fatalf("status error must not carry the query string: %s", se.Error())
	}
	if !strings.Contains(se.Error(), ts.URL+"/boom") {
		t.Fatalf("status error should keep the path: %s", se.Error())
	}
}

func TestGetJSONRejectsOversizedResponse(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"data":"`+strings.Repeat("x", 10<<20)+`"}`)
	}))
	defer ts.Close()

	var result struct {
		Data string `json:"data"`
	}
	if err := GetJSON(context.Background(), ts.Client(), ts.URL, "", &result); err == nil {
		t.Fatal("expected oversized JSON response to fail decoding")
	}
}

func TestGetJSONDecodesSuccessfulResponse(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", r.Method)
		}
		_, _ = w.Write([]byte(`{"name":"example"}`))
	}))
	defer ts.Close()

	var result struct {
		Name string `json:"name"`
	}
	if err := GetJSON(context.Background(), ts.Client(), ts.URL, "", &result); err != nil {
		t.Fatalf("GetJSON failed: %v", err)
	}
	if result.Name != "example" {
		t.Fatalf("decoded name = %q, want example", result.Name)
	}
}

func TestGetJSONSetsUserAgent(t *testing.T) {
	const wantUserAgent = "parser-test/1.0"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got != wantUserAgent {
			t.Errorf("User-Agent = %q, want %q", got, wantUserAgent)
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer ts.Close()

	var result struct {
		OK bool `json:"ok"`
	}
	if err := GetJSON(context.Background(), ts.Client(), ts.URL, wantUserAgent, &result); err != nil {
		t.Fatalf("GetJSON failed: %v", err)
	}
}

func TestGetJSONRejectsUnexpectedStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer ts.Close()

	var result struct{}
	err := GetJSON(context.Background(), ts.Client(), ts.URL, "", &result)
	if err == nil {
		t.Fatal("GetJSON accepted non-2xx response")
	}
	if got := err.Error(); got != "unexpected status 404 from "+ts.URL {
		t.Fatalf("error = %q, want unexpected status", got)
	}
}

func TestGetJSONRejectsInvalidJSON(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer ts.Close()

	var result struct{}
	if err := GetJSON(context.Background(), ts.Client(), ts.URL, "", &result); err == nil {
		t.Fatal("GetJSON accepted invalid JSON")
	}
}

func TestPostJSONSendsHeadersAndDecodesResponse(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", got)
		}
		body, _ := io.ReadAll(r.Body)
		if got := string(body); got != `{"id":"test"}` {
			t.Errorf("body = %q, want JSON body", got)
		}
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer ts.Close()

	var result struct {
		OK bool `json:"ok"`
	}
	err := PostJSON(context.Background(), ts.Client(), ts.URL,
		map[string]string{"Content-Type": "application/json"},
		[]byte(`{"id":"test"}`), &result)
	if err != nil {
		t.Fatalf("PostJSON failed: %v", err)
	}
	if !result.OK {
		t.Fatal("response was not decoded")
	}
}

func TestPostJSONRejectsUnexpectedStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer ts.Close()

	var result struct{}
	err := PostJSON(context.Background(), ts.Client(), ts.URL, nil, nil, &result)
	if err == nil {
		t.Fatal("PostJSON accepted non-2xx response")
	}
	var statusErr *HTTPStatusError
	if !errors.As(err, &statusErr) || statusErr.StatusCode != http.StatusNotFound {
		t.Fatalf("error = %v, want HTTPStatusError 404", err)
	}
}

func TestGetDocSetsUserAgentAndLimitsBody(t *testing.T) {
	const userAgent = "doc-test/1.0"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("User-Agent"); got != userAgent {
			t.Errorf("User-Agent = %q, want %q", got, userAgent)
		}
		_, _ = io.Copy(w, strings.NewReader(strings.Repeat("x", 32<<20+1)))
	}))
	defer ts.Close()

	reader, err := GetDoc(context.Background(), ts.Client(), ts.URL, userAgent)
	if err != nil {
		t.Fatalf("GetDoc failed: %v", err)
	}
	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read document: %v", err)
	}
	if len(body) != 32<<20 {
		t.Fatalf("body length = %d, want %d", len(body), 32<<20)
	}
}

func TestPublicClientBlocksPrivateRedirect(t *testing.T) {
	// Allowlist entries are host-scoped: allowing a host permits every port
	// on it, so a redirect to a different port on an allowed host is no
	// longer blocked. Redirect to a private address that is not allowlisted
	// to verify private redirect targets are still denied.
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://10.0.0.1/", http.StatusFound)
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

	client, err := NewPublicStreamingClientWithConcurrency(1, 50*time.Millisecond, ts.URL)
	if err != nil {
		t.Fatalf("NewPublicStreamingClientWithConcurrency failed: %v", err)
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

func TestPublicClientWithProxyBlocksPrivateTarget(t *testing.T) {
	client, err := NewPublicClientWithProxy(time.Second, "http://127.0.0.1:1")
	if err != nil {
		t.Fatalf("NewPublicClientWithProxy failed: %v", err)
	}

	if _, err := client.Get("http://169.254.169.254/"); err == nil || !strings.Contains(err.Error(), "not public") {
		t.Fatalf("private target error = %v, want not public error", err)
	}
}

func TestPublicClientRejectsHTTPSDowngradeRedirect(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://example.com/", http.StatusFound)
	}))
	defer server.Close()

	client, err := NewPublicClient(time.Second, server.URL)
	if err != nil {
		t.Fatalf("NewPublicClient failed: %v", err)
	}
	transport := client.Transport.(*http.Transport)
	transport.TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig

	if _, err := client.Get(server.URL); err == nil || !strings.Contains(err.Error(), "https-to-http") {
		t.Fatalf("HTTPS downgrade error = %v, want https-to-http error", err)
	}
}

func TestPublicClientWithPrivateProxyHost(t *testing.T) {
	var gotTarget string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTarget = r.URL.String()
		_, _ = w.Write([]byte("proxied"))
	}))
	defer proxy.Close()

	client, err := NewPublicClientWithProxy(time.Second, proxy.URL)
	if err != nil {
		t.Fatalf("NewPublicClientWithProxy failed: %v", err)
	}
	resp, err := client.Get("http://example.com/page")
	if err != nil {
		t.Fatalf("request through local proxy failed: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "proxied" {
		t.Fatalf("body = %q, want proxied", body)
	}
	if gotTarget != "http://example.com/page" {
		t.Fatalf("proxy saw target %q, want http://example.com/page", gotTarget)
	}
}
