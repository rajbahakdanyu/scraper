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
)

func ParseNetscapeCookies(path string) ([]*http.Cookie, error) {
	f, err := os.Open(path)
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
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.Split(line, "\t")
		if len(parts) < 7 {
			fmt.Fprintf(os.Stderr, "cookies:%d: malformed line (expected 7 tab-separated fields), skipping\n", lineNo)
			continue
		}

		domain := parts[0]
		path := parts[2]
		secure := strings.EqualFold(parts[3], "TRUE")
		expiryStr := parts[4]
		name := parts[5]
		value := parts[6]

		expiry, err := strconv.ParseInt(expiryStr, 10, 64)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cookies:%d: cannot parse expiry %q, skipping cookie %s\n", lineNo, expiryStr, name)
			continue
		}
		if time.Now().Unix() > expiry {
			fmt.Fprintf(os.Stderr, "cookies:%d: expired cookie %s, skipping\n", lineNo, name)
			continue
		}

		cookie := &http.Cookie{
			Name:    name,
			Value:   value,
			Domain:  domain,
			Path:    path,
			Secure:  secure,
			Expires: time.Unix(expiry, 0),
		}
		cookies = append(cookies, cookie)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read cookies file: %w", err)
	}
	return cookies, nil
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
		jar.SetCookies(&url.URL{Scheme: "https", Host: host}, []*http.Cookie{c})
	}
	clone := *client
	clone.Jar = jar
	return &clone
}
