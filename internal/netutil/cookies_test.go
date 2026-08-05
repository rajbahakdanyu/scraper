package netutil

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

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
	u, _ := url.Parse("http://example.com/")
	jarCookies := client.Jar.Cookies(u)
	if len(jarCookies) == 0 {
		t.Fatal("expected cookies in jar, got none")
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
