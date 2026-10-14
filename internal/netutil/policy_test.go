package netutil

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
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

func TestAllowlistEntryWithoutPortMatchesAnyPort(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "ok")
	}))
	defer ts.Close()

	// Strip the port from the test server's URL to build a port-less
	// allowlist entry — previously this could never match a dial to
	// 127.0.0.1:<ts port>.
	u, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	portLess := u.Scheme + "://" + u.Hostname()

	client, err := NewPublicClient(time.Second, portLess)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := client.Get(ts.URL)
	if err != nil {
		t.Fatalf("request through port-less allowlist failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

func TestPrivateProxyHostIsNotAllowedAsTarget(t *testing.T) {
	client, err := NewPublicClientWithProxy(time.Second, "http://127.0.0.1:1")
	if err != nil {
		t.Fatalf("NewPublicClientWithProxy failed: %v", err)
	}
	// The proxy host is dialable, but must not become a valid request target.
	if _, err := client.Get("http://127.0.0.1:1/"); err == nil || !strings.Contains(err.Error(), "not public") {
		t.Fatalf("private target via proxy error = %v, want not public error", err)
	}
}

func TestStrictPolicyAllowsListedHostAndSubdomains(t *testing.T) {
	policy := NewStrictPolicy("https://filester.sh")
	for _, rawURL := range []string{
		"https://filester.sh/f/1",
		"https://CDN.FilESTer.sh:8443/x",
		"https://cdn.filester.sh/x",
	} {
		if err := policy.ValidateURL(rawURL); err != nil {
			t.Errorf("ValidateURL(%s) = %v, want nil", rawURL, err)
		}
	}
}

func TestStrictPolicyRejectsUnlistedPublicHosts(t *testing.T) {
	policy := NewStrictPolicy("https://filester.sh")
	for _, rawURL := range []string{
		"https://evil.example.net/x",
		"https://filester.sh.evil.example.net/x",
		"https://notfilester.sh/x",
	} {
		if err := policy.ValidateURL(rawURL); err == nil {
			t.Errorf("ValidateURL(%s) accepted an unlisted host", rawURL)
		}
	}
}

func TestWithRedirectPolicyBlocksUnlistedRedirect(t *testing.T) {
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://localhost:9/unlisted", http.StatusFound)
	}))
	defer redirector.Close()

	client := WithRedirectPolicy(redirector.Client(), NewStrictPolicy(redirector.URL))
	if _, err := client.Get(redirector.URL); err == nil || !strings.Contains(err.Error(), "allowlist") {
		t.Fatalf("redirect error = %v, want allowlist rejection", err)
	}
}

func TestWithRedirectPolicyAllowsAllowlistedRedirect(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("target"))
	}))
	defer target.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redirector.Close()

	client := WithRedirectPolicy(redirector.Client(), NewStrictPolicy(redirector.URL))
	resp, err := client.Get(redirector.URL)
	if err != nil {
		t.Fatalf("allowlisted redirect failed: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "target" {
		t.Fatalf("body = %q, want target", body)
	}
}

func TestIsPublicIPRejectsSpecialPurposeRanges(t *testing.T) {
	blocked := []string{
		"100.64.0.1",      // CGNAT (RFC 6598)
		"100.127.255.254", // CGNAT upper bound
		"192.0.2.1",       // TEST-NET-1
		"198.18.0.1",      // benchmarking (RFC 2544)
		"198.51.100.1",    // TEST-NET-2
		"203.0.113.1",     // TEST-NET-3
		"240.0.0.1",       // reserved (RFC 1112)
		"255.255.255.255", // broadcast
	}
	for _, addr := range blocked {
		if isPublicIP(net.ParseIP(addr)) {
			t.Errorf("isPublicIP(%s) = true, want false", addr)
		}
	}
	allowed := []string{
		"1.1.1.1",
		"8.8.8.8",
		"100.128.0.1", // just past CGNAT
		"203.0.114.1", // just past TEST-NET-3
		"223.255.255.255",
	}
	for _, addr := range allowed {
		if !isPublicIP(net.ParseIP(addr)) {
			t.Errorf("isPublicIP(%s) = false, want true", addr)
		}
	}

	blockedV6 := []string{
		"2001:db8::1", // documentation (RFC 3849)
		"3fff::1",     // documentation (RFC 9637)
		"100::1",      // discard-only (RFC 6666)
		"2001:2::1",   // benchmarking (RFC 5180)
		"64:ff9b::7f00:1",
		"64:ff9b:1::a00:1",
		"2002:c0a8:0101::1", // 6to4 wrapping 192.168.1.1
	}
	for _, addr := range blockedV6 {
		if isPublicIP(net.ParseIP(addr)) {
			t.Errorf("isPublicIP(%s) = true, want false", addr)
		}
	}
	allowedV6 := []string{
		"2606:4700:4700::1111",
		"2001:4860:4860::8888",
	}
	for _, addr := range allowedV6 {
		if !isPublicIP(net.ParseIP(addr)) {
			t.Errorf("isPublicIP(%s) = false, want true", addr)
		}
	}
}

func TestStrictPolicyNormalizesTrailingDotAllowlist(t *testing.T) {
	p := NewStrictPolicy("https://host.")
	if err := p.ValidateURL("https://host/file"); err != nil {
		t.Fatalf("trailing-dot allowlist entry should match the host: %v", err)
	}
}
