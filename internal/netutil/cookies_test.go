package netutil

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	fn()
	_ = w.Close()
	os.Stderr = old
	output, err := io.ReadAll(r)
	_ = r.Close()
	if err != nil {
		t.Fatal(err)
	}
	return string(output)
}

func TestParseNetscapeCookiesValid(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies.txt")
	content := "# Netscape HTTP Cookie File\n" +
		".example.com\tTRUE\t/\tFALSE\t9999999999\tsession\tabc123\n" +
		".example.com\tTRUE\t/\tFALSE\t9999999999\ttoken\txyz789\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	cookies, err := ParseNetscapeCookies(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cookies) != 2 {
		t.Fatalf("expected 2 cookies, got %d", len(cookies))
	}
	if cookies[0].Name != "session" || cookies[0].Value != "abc123" {
		t.Errorf("cookie 0: got %s=%s, want session=abc123", cookies[0].Name, cookies[0].Value)
	}
	if cookies[1].Name != "token" || cookies[1].Value != "xyz789" {
		t.Errorf("cookie 1: got %s=%s, want token=xyz789", cookies[1].Name, cookies[1].Value)
	}
}

func TestParseNetscapeCookiesWarnsForGroupOrOtherReadableFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies.txt")
	if err := os.WriteFile(path, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	stderr := captureStderr(t, func() {
		if _, err := ParseNetscapeCookies(path); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(stderr, "permissions") {
		t.Fatalf("expected permissions warning, got %q", stderr)
	}
}

func TestParseNetscapeCookiesEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.txt")
	if err := os.WriteFile(path, []byte(""), 0600); err != nil {
		t.Fatal(err)
	}
	cookies, err := ParseNetscapeCookies(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cookies) != 0 {
		t.Errorf("expected 0 cookies, got %d", len(cookies))
	}
}

func TestParseNetscapeCookiesFileNotFound(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing.txt")
	_, err := ParseNetscapeCookies(path)
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestParseNetscapeCookiesSkipsComments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "comments.txt")
	content := "# header comment\n" +
		"# another comment\n" +
		".example.com\tTRUE\t/\tTRUE\t9999999999\tauth\ttoken\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	cookies, err := ParseNetscapeCookies(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cookies) != 1 {
		t.Fatalf("expected 1 cookie, got %d", len(cookies))
	}
	if cookies[0].Name != "auth" {
		t.Errorf("got name %s, want auth", cookies[0].Name)
	}
}

func TestParseNetscapeCookiesSkipsBlankLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "blanks.txt")
	content := "\n" +
		".example.com\tTRUE\t/\tFALSE\t9999999999\tfoo\tbar\n" +
		"\n\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	cookies, err := ParseNetscapeCookies(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cookies) != 1 {
		t.Fatalf("expected 1 cookie, got %d", len(cookies))
	}
}

func TestParseNetscapeCookiesExpiredSkipped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "expired.txt")
	expired := time.Now().Unix() - 3600
	content := ".example.com\tTRUE\t/\tFALSE\t" +
		strconv.FormatInt(expired, 10) + "\texpired_cookie\toldval\n" +
		".example.com\tTRUE\t/\tFALSE\t9999999999\tvalid_cookie\tgoodval\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	cookies, err := ParseNetscapeCookies(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cookies) != 1 {
		t.Fatalf("expected 1 cookie (expired skipped), got %d", len(cookies))
	}
	if cookies[0].Name != "valid_cookie" {
		t.Errorf("got %s, want valid_cookie", cookies[0].Name)
	}
}

func TestParseNetscapeCookiesMalformedLineSkipped(t *testing.T) {
	path := filepath.Join(t.TempDir(), "malformed.txt")
	content := "garbageline\n" +
		".example.com\tTRUE\t/\tFALSE\t9999999999\tgood\tvalue\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	cookies, err := ParseNetscapeCookies(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cookies) != 1 {
		t.Fatalf("expected 1 cookie, got %d", len(cookies))
	}
}

func TestParseNetscapeCookiesKeepsSessionCookies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.txt")
	line := ".example.com\tTRUE\t/\tTRUE\t0\tsession\tabc\n"
	if err := os.WriteFile(path, []byte(line), 0600); err != nil {
		t.Fatal(err)
	}
	cookies, err := ParseNetscapeCookies(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cookies) != 1 || cookies[0].Name != "session" {
		t.Fatalf("session cookie (expiry 0) must be kept, got %+v", cookies)
	}
	if !cookies[0].Expires.IsZero() {
		t.Fatalf("session cookie must not carry an Expires timestamp, got %v", cookies[0].Expires)
	}
}

func TestParseNetscapeCookiesSecureFlag(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secure.txt")
	content := ".example.com\tTRUE\t/\tTRUE\t9999999999\tsecure_cookie\tsecret\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	cookies, err := ParseNetscapeCookies(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cookies) != 1 {
		t.Fatalf("expected 1 cookie, got %d", len(cookies))
	}
	if !cookies[0].Secure {
		t.Error("expected Secure flag to be true")
	}
}

func TestParseNetscapeCookiesAlwaysSecure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "insecure.txt")
	content := ".example.com\tTRUE\t/\tFALSE\t9999999999\tsid\tsecret\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	cookies, err := ParseNetscapeCookies(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cookies) != 1 || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie = %+v, want Secure, HttpOnly and SameSite=Lax", cookies)
	}
}

func TestParseNetscapeCookiesSubdomainFlagHandling(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subdomain.txt")
	content := ".example.com\tTRUE\t/\tFALSE\t9999999999\tdotprefix\tval\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	cookies, err := ParseNetscapeCookies(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cookies[0].Domain != ".example.com" {
		t.Errorf("expected Domain .example.com, got %s", cookies[0].Domain)
	}
	if cookies[0].Path != "/" {
		t.Errorf("expected Path /, got %s", cookies[0].Path)
	}
}

func TestParseNetscapeCookiesHttpOnlyPrefix(t *testing.T) {
	path := filepath.Join(t.TempDir(), "httponly.txt")
	content := "# Netscape HTTP Cookie File\n" +
		"#HttpOnly_.example.com\tTRUE\t/\tTRUE\t9999999999\tsession\tsecret\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	cookies, err := ParseNetscapeCookies(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cookies) != 1 || cookies[0].Name != "session" || cookies[0].Domain != ".example.com" {
		t.Fatalf("#HttpOnly_ line must be parsed as a cookie, got %+v", cookies)
	}
}

func TestWithCookiesHonorsIncludeSubdomains(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scope.txt")
	content := "host.example.com\tFALSE\t/\tFALSE\t9999999999\thostonly\t1\n" +
		"example.com\tTRUE\t/\tFALSE\t9999999999\tdomain\t1\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	cookies, err := ParseNetscapeCookies(path)
	if err != nil {
		t.Fatal(err)
	}
	jar := WithCookies(&http.Client{}, cookies).Jar
	names := func(raw string) []string {
		u, _ := url.Parse(raw)
		var out []string
		for _, c := range jar.Cookies(u) {
			out = append(out, c.Name)
		}
		return out
	}
	if got := names("https://host.example.com/"); len(got) != 2 {
		t.Fatalf("host.example.com cookies = %v, want hostonly and domain", got)
	}
	if got := names("https://sub.host.example.com/"); len(got) != 1 || got[0] != "domain" {
		t.Fatalf("a host-only cookie must not reach subdomains, got %v", got)
	}
}

func TestParseNetscapeCookiesExpiryAsInt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "int_expiry.txt")
	content := ".example.com\tTRUE\t/\tFALSE\t9999999999\tint_cookie\tintval\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	cookies, err := ParseNetscapeCookies(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cookies) != 1 {
		t.Fatalf("expected 1 cookie, got %d", len(cookies))
	}
}

func TestWithCookiesSetsJar(t *testing.T) {
	cookies := []*http.Cookie{
		{Name: "session", Value: "abc", Domain: "example.com"},
	}
	client := WithCookies(&http.Client{}, cookies)
	if client.Jar == nil {
		t.Fatal("expected client.Jar to be set")
	}
	u, _ := url.Parse("https://example.com/")
	jarCookies := client.Jar.Cookies(u)
	if len(jarCookies) == 0 {
		t.Fatal("expected cookies in jar, got none")
	}
}

// Imported cookies are always Secure: a plain-HTTP request to the same host
// must not carry them, even when the source file marked them non-secure.
func TestWithCookiesNeverSendsCookiesOverPlainHTTP(t *testing.T) {
	client := WithCookies(&http.Client{}, []*http.Cookie{{Name: "session", Value: "abc", Domain: "example.com", Secure: false}})
	if got := client.Jar.Cookies(&url.URL{Scheme: "http", Host: "example.com", Path: "/"}); len(got) != 0 {
		t.Fatalf("plain-HTTP request would send %v", got)
	}
}

func TestWithCookiesSendsCookiesOverHTTPS(t *testing.T) {
	var got string
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie("session"); err == nil {
			got = c.Value
		}
	}))
	defer ts.Close()

	u, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatal(err)
	}
	cookies := []*http.Cookie{{Name: "session", Value: "abc", Domain: u.Hostname(), Path: "/"}}
	client := WithCookies(ts.Client(), cookies)

	resp, err := client.Get(ts.URL)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	resp.Body.Close()
	if got != "abc" {
		t.Fatalf("https request saw cookie %q, want abc", got)
	}
}

func TestWithCookiesEmptySliceNoop(t *testing.T) {
	client := WithCookies(&http.Client{}, nil)
	if client.Jar != nil {
		t.Error("expected no jar when cookies slice is nil")
	}
	client = WithCookies(&http.Client{}, []*http.Cookie{})
	if client.Jar != nil {
		t.Error("expected no jar when cookies slice is empty")
	}
}

func TestWithCookiesPreservesExistingClient(t *testing.T) {
	original := &http.Client{Timeout: 42 * time.Second}
	cookies := []*http.Cookie{{Name: "a", Value: "b"}}
	clone := WithCookies(original, cookies)
	if clone.Timeout != 42*time.Second {
		t.Errorf("expected timeout 42s, got %v", clone.Timeout)
	}
	if original.Jar != nil {
		t.Error("original client should not be modified")
	}
}
