package netutil

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type HTTPStatusError struct {
	StatusCode int
	URL        string
}

func (e *HTTPStatusError) Error() string {
	return fmt.Sprintf("unexpected status %d from %s", e.StatusCode, e.URL)
}

// newHTTPStatusError builds a status error whose URL never carries
// userinfo, query, or fragment: signed tokens and credentials must not
// reach error messages, logs, or retry reports.
func newHTTPStatusError(statusCode int, rawURL string) *HTTPStatusError {
	u, err := url.Parse(rawURL)
	if err != nil {
		return &HTTPStatusError{StatusCode: statusCode, URL: rawURL}
	}
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	return &HTTPStatusError{StatusCode: statusCode, URL: u.String()}
}

// GetJSON performs a GET request to url with the given client, checks for a
// 2xx response, and decodes the JSON body into target.
func GetJSON(ctx context.Context, client *http.Client, url, userAgent string, target interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return newHTTPStatusError(resp.StatusCode, url)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 10<<20)).Decode(target); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// PostJSON performs a POST request with the given headers, checks for a 2xx
// response, and decodes the JSON body into target.
func PostJSON(ctx context.Context, client *http.Client, url string, headers map[string]string, body []byte, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return newHTTPStatusError(resp.StatusCode, url)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 10<<20)).Decode(target); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

// GetDoc fetches a document and returns its body limited to 32 MiB.
func GetDoc(ctx context.Context, client *http.Client, url, userAgent string) (io.Reader, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	if userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, newHTTPStatusError(resp.StatusCode, url)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	return bytes.NewReader(body), nil
}

type Policy struct {
	allowedPrivateHosts map[string]struct{}
	allowedDialHosts    map[string]struct{}
	strict              bool
	resolver            *net.Resolver
}

// normalizeHost lowercases a host and strips the DNS root label (a trailing
// dot), matching how request hosts are normalized before comparison.
func normalizeHost(host string) string {
	return strings.TrimSuffix(strings.ToLower(host), ".")
}

// NewStrictPolicy returns a Policy that, in addition to the public-IP dial
// guard, only accepts request URLs whose host is in allowedURLs or is a
// subdomain of one. Use it where a caller intends to pin downloads to known
// infrastructure; NewPolicy stays permissive (public hosts anywhere).
func NewStrictPolicy(allowedURLs ...string) *Policy {
	p := NewPolicy(allowedURLs...)
	p.strict = true
	return p
}

func NewPolicy(allowedPrivateURLs ...string) *Policy {
	allowed := make(map[string]struct{}, len(allowedPrivateURLs))
	for _, rawURL := range allowedPrivateURLs {
		u, err := url.Parse(rawURL)
		if err == nil && u.Host != "" {
			allowed[normalizeHost(u.Hostname())] = struct{}{}
		}
	}
	return &Policy{allowedPrivateHosts: allowed, resolver: net.DefaultResolver}
}

// allowDialHost permits dialing host without permitting it as a request
// target. Used for a configured proxy host (which is dialed directly by the
// transport, never fetched as the request URL): a local/private proxy would
// otherwise be rejected by the public-IP dial guard.
func (p *Policy) allowDialHost(host string) {
	if p.allowedDialHosts == nil {
		p.allowedDialHosts = make(map[string]struct{})
	}
	p.allowedDialHosts[normalizeHost(host)] = struct{}{}
}

func (p *Policy) dialAllowed(host string) bool {
	host = strings.ToLower(host)
	if _, ok := p.allowedPrivateHosts[host]; ok {
		return true
	}
	_, ok := p.allowedDialHosts[host]
	return ok
}

func (p *Policy) ValidateURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL %q: %w", rawURL, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("URL %q must use http or https", rawURL)
	}
	if u.Hostname() == "" {
		return fmt.Errorf("URL %q has no host", rawURL)
	}
	if u.User != nil {
		return fmt.Errorf("URL %q must not contain credentials", rawURL)
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if p.strict {
		if !p.allowsHost(host) {
			return fmt.Errorf("host %q is not in the allowlist", host)
		}
		return nil
	}
	if _, ok := p.allowedPrivateHosts[host]; ok {
		return nil
	}
	return p.validateHost(u.Hostname())
}

func (p *Policy) allowsHost(host string) bool {
	for allowed := range p.allowedPrivateHosts {
		if host == allowed || strings.HasSuffix(host, "."+allowed) {
			return true
		}
	}
	return false
}

func (p *Policy) validateHost(host string) error {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return fmt.Errorf("host %q is not public", host)
	}
	if ip := net.ParseIP(host); ip != nil && !isPublicIP(ip) {
		return fmt.Errorf("host %q is not public", host)
	}
	return nil
}

func (p *Policy) dialContext(dialer *net.Dialer, readTimeout time.Duration) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		if p.dialAllowed(host) {
			conn, err := dialer.DialContext(ctx, network, address)
			return withReadTimeout(conn, readTimeout), err
		}

		addresses, err := p.resolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		var lastErr error
		for _, address := range addresses {
			if !isPublicIP(address.IP) {
				continue
			}
			conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(address.IP.String(), port))
			if err == nil {
				return withReadTimeout(conn, readTimeout), nil
			}
			lastErr = err
		}
		if lastErr != nil {
			return nil, lastErr
		}
		return nil, fmt.Errorf("host %q did not resolve to a public address", host)
	}
}

func NewPublicStreamingClient(responseHeaderTimeout time.Duration, allowedPrivateURLs ...string) (*http.Client, error) {
	return newPublicClientWithConcurrency(1, 0, responseHeaderTimeout, responseHeaderTimeout, "", allowedPrivateURLs...)
}

// BrowserUserAgent is what the site scrapers send; several hosts reject
// non-browser agents on their page and API endpoints.
const BrowserUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36"

// ScraperTimeout bounds each scraper page/API request.
const ScraperTimeout = 30 * time.Second

// NewScraperClient returns the public-network client a site scraper uses,
// routed through proxyURL when set and carrying cookies when given. It can
// only fail on an invalid proxy URL.
func NewScraperClient(proxyURL string, cookies []*http.Cookie) (*http.Client, error) {
	client, err := newPublicClientWithConcurrency(1, ScraperTimeout, ScraperTimeout, ScraperTimeout, proxyURL)
	if err != nil {
		return nil, err
	}
	return WithCookies(client, cookies), nil
}

func NewPublicStreamingClientWithProxy(responseHeaderTimeout time.Duration, proxyURL string) (*http.Client, error) {
	return newPublicClientWithConcurrency(1, 0, responseHeaderTimeout, responseHeaderTimeout, proxyURL)
}

// WithRedirectPolicy returns a shallow clone of client whose redirects are
// additionally validated against policy. The client's existing
// CheckRedirect (redirect cap, HTTPS-downgrade block, public-IP policy)
// runs first and is preserved. Use this to keep a strict host allowlist in
// force across redirects, not just for the initial URL.
func WithRedirectPolicy(client *http.Client, policy *Policy) *http.Client {
	clone := *client
	existing := client.CheckRedirect
	clone.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if existing != nil {
			if err := existing(req, via); err != nil {
				return err
			}
		}
		return policy.ValidateURL(req.URL.String())
	}
	return &clone
}

func WithUserAgent(client *http.Client, userAgent string) *http.Client {
	if userAgent == "" {
		return client
	}
	clone := *client
	clone.Transport = &userAgentTransport{base: client.Transport, userAgent: userAgent}
	return &clone
}

type userAgentTransport struct {
	base      http.RoundTripper
	userAgent string
}

func (t *userAgentTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header = req.Header.Clone()
	if clone.Header.Get("User-Agent") == "" {
		clone.Header.Set("User-Agent", t.userAgent)
	}
	return t.base.RoundTrip(clone)
}

func NewPublicStreamingClientWithConcurrency(concurrency int, responseHeaderTimeout time.Duration, allowedPrivateURLs ...string) (*http.Client, error) {
	return newPublicClientWithConcurrency(concurrency, 0, responseHeaderTimeout, responseHeaderTimeout, "", allowedPrivateURLs...)
}

func NewPublicStreamingClientWithProxyAndConcurrency(concurrency int, responseHeaderTimeout time.Duration, proxyURL string) (*http.Client, error) {
	return newPublicClientWithConcurrency(concurrency, 0, responseHeaderTimeout, responseHeaderTimeout, proxyURL)
}

func newPublicClientWithConcurrency(concurrency int, timeout, responseHeaderTimeout, readTimeout time.Duration, proxyURL string, allowedPrivateURLs ...string) (*http.Client, error) {
	if concurrency < 1 {
		concurrency = 1
	}
	policy := NewPolicy(allowedPrivateURLs...)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConnsPerHost = concurrency + 2
	transport.MaxIdleConns = 2 * concurrency
	transport.Proxy = nil
	if proxyURL != "" {
		u, err := url.Parse(proxyURL)
		if err != nil {
			return nil, fmt.Errorf("invalid proxy URL %q: %w", proxyURL, err)
		}
		if u.Hostname() == "" {
			return nil, fmt.Errorf("invalid proxy URL %q: missing host", proxyURL)
		}
		policy.allowDialHost(u.Hostname())
		proxy := http.ProxyURL(u)
		transport.Proxy = func(req *http.Request) (*url.URL, error) {
			if err := policy.ValidateURL(req.URL.String()); err != nil {
				return nil, err
			}
			return proxy(req)
		}
	}
	transport.ResponseHeaderTimeout = responseHeaderTimeout
	transport.DialContext = policy.dialContext(&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}, readTimeout)

	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("stopped after 10 redirects")
			}
			if err := policy.ValidateURL(req.URL.String()); err != nil {
				return err
			}
			if len(via) > 0 && via[len(via)-1].URL.Scheme == "https" && req.URL.Scheme == "http" {
				return fmt.Errorf("https-to-http redirect denied for %q", req.URL.String())
			}
			return nil
		},
	}, nil
}

type readTimeoutConn struct {
	net.Conn
	timeout time.Duration
}

func (c *readTimeoutConn) Read(buffer []byte) (int, error) {
	if c.timeout > 0 {
		if err := c.SetReadDeadline(time.Now().Add(c.timeout)); err != nil {
			return 0, err
		}
	}
	return c.Conn.Read(buffer)
}

func withReadTimeout(conn net.Conn, timeout time.Duration) net.Conn {
	if conn == nil || timeout <= 0 {
		return conn
	}
	return &readTimeoutConn{Conn: conn, timeout: timeout}
}

// nonPublicIPBlocks are IANA special-purpose ranges that Go's
// IsGlobalUnicast/IsPrivate predicates do not cover: CGNAT, TEST-NETs,
// benchmarking, and the reserved 240/4 block including broadcast for IPv4;
// documentation, discard-only, and benchmarking ranges for IPv6.
var nonPublicIPBlocks = func() []*net.IPNet {
	cidrs := []string{
		"100.64.0.0/10",   // CGNAT (RFC 6598)
		"192.0.2.0/24",    // TEST-NET-1
		"198.18.0.0/15",   // benchmarking (RFC 2544)
		"198.51.100.0/24", // TEST-NET-2
		"203.0.113.0/24",  // TEST-NET-3
		"240.0.0.0/4",     // reserved + broadcast
		"100::/64",        // discard-only (RFC 6666)
		"2001:2::/48",     // benchmarking (RFC 5180)
		"2001:db8::/32",   // documentation (RFC 3849)
		"3fff::/20",       // documentation (RFC 9637)
		// IPv6 ranges that embed an IPv4 address a gateway would translate
		// to, possibly a private one (64:ff9b::7f00:1 -> 127.0.0.1).
		"64:ff9b::/96",   // NAT64 well-known prefix (RFC 6052)
		"64:ff9b:1::/48", // NAT64 local-use (RFC 8215)
		"2002::/16",      // 6to4 (RFC 3056)
	}
	blocks := make([]*net.IPNet, 0, len(cidrs))
	for _, cidr := range cidrs {
		_, block, err := net.ParseCIDR(cidr)
		if err != nil {
			panic(fmt.Sprintf("invalid built-in CIDR %q: %v", cidr, err))
		}
		blocks = append(blocks, block)
	}
	return blocks
}()

func isPublicIP(ip net.IP) bool {
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
		return false
	}
	for _, block := range nonPublicIPBlocks {
		if block.Contains(ip) {
			return false
		}
	}
	return true
}
