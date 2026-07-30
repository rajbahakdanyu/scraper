package netutil

import (
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

	client := NewPublicClient(time.Second, ts.URL)
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

	client := NewPublicClient(time.Second, redirector.URL)
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

	client := WithUserAgent(NewPublicClient(time.Second, ts.URL), "Custom/1.0")
	resp, err := client.Get(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got != "Custom/1.0" {
		t.Fatalf("User-Agent = %q", got)
	}
}
