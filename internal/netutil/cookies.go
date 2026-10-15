package netutil

import (
	"bufio"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/rajbahakdanyu/scraper/internal/fileutil"
)

func ParseNetscapeCookies(path string) ([]*http.Cookie, error) {
	if fi, err := os.Stat(path); err == nil && fi.Mode().Perm()&0077 != 0 {
		fmt.Fprintf(os.Stderr, "warning: cookies file %q has group or other permissions (%04o)\n", path, fi.Mode().Perm())
	}
	f, err := fileutil.OpenUserFile(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var cookies []*http.Cookie
	scanner := bufio.NewScanner(f)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		// curl/yt-dlp/browser exports mark HttpOnly cookies (often the
		// session cookie) with this prefix rather than as a comment.
		line = strings.TrimPrefix(line, "#HttpOnly_")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.Split(line, "\t")
		if len(parts) < 7 {
			fmt.Fprintf(os.Stderr, "cookies:%d: malformed line (expected 7 tab-separated fields), skipping\n", lineNo)
			continue
		}

		domain := strings.TrimPrefix(parts[0], ".")
		// A leading dot marks a domain cookie; none means host-only (see
		// WithCookies).
		if strings.EqualFold(parts[1], "TRUE") {
			domain = "." + domain
		}
		path := parts[2]
		expiryStr := parts[4]
		name := parts[5]
		value := parts[6]

		expiry, err := strconv.ParseInt(expiryStr, 10, 64)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cookies:%d: cannot parse expiry %q, skipping cookie %s\n", lineNo, expiryStr, name)
			continue
		}
		// Expiry 0 is the Netscape format's session-cookie convention:
		// no timestamp, so it must not be treated as already expired.
		if expiry != 0 && time.Now().Unix() > expiry {
			fmt.Fprintf(os.Stderr, "cookies:%d: expired cookie %s, skipping\n", lineNo, name)
			continue
		}

		var expires time.Time
		if expiry != 0 {
			expires = time.Unix(expiry, 0)
		}
		cookies = append(cookies, NewCookie(name, value, domain, path, expires))
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read cookies file: %w", err)
	}
	return cookies, nil
}

// NewCookie builds a cookie for an outbound jar. Session cookies for the
// supported sites must never travel over plain HTTP, so every cookie is
// Secure regardless of the flag in its source.
func NewCookie(name, value, domain, path string, expires time.Time) *http.Cookie {
	return &http.Cookie{
		Name:     name,
		Value:    value,
		Domain:   domain,
		Path:     path,
		Expires:  expires,
		Secure:   true,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}
}

func WithCookies(client *http.Client, cookies []*http.Cookie) *http.Client {
	if len(cookies) == 0 {
		return client
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		// unreachable: cookiejar.New(nil) never returns an error
		panic(fmt.Sprintf("cookiejar.New: %v", err))
	}
	for _, c := range cookies {
		host := strings.TrimPrefix(c.Domain, ".")
		domain := c.Domain
		if !strings.HasPrefix(domain, ".") {
			domain = ""
		}
		jar.SetCookies(&url.URL{Scheme: "https", Host: host}, []*http.Cookie{NewCookie(c.Name, c.Value, domain, c.Path, c.Expires)})
	}
	clone := *client
	clone.Jar = jar
	return &clone
}
