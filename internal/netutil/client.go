package netutil

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Policy struct {
	allowedPrivateHosts map[string]struct{}
	resolver            *net.Resolver
}

func NewPolicy(allowedPrivateURLs ...string) *Policy {
	allowed := make(map[string]struct{}, len(allowedPrivateURLs))
	for _, rawURL := range allowedPrivateURLs {
		u, err := url.Parse(rawURL)
		if err == nil && u.Host != "" {
			allowed[strings.ToLower(u.Host)] = struct{}{}
		}
	}
	return &Policy{allowedPrivateHosts: allowed, resolver: net.DefaultResolver}
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
	if _, ok := p.allowedPrivateHosts[strings.ToLower(u.Host)]; ok {
		return nil
	}
	return p.validateHost(u.Hostname())
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
		if _, ok := p.allowedPrivateHosts[strings.ToLower(address)]; ok {
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

func NewPublicClient(timeout time.Duration, allowedPrivateURLs ...string) (*http.Client, error) {
	return newPublicClient(timeout, timeout, timeout, "", allowedPrivateURLs...)
}

func NewPublicStreamingClient(responseHeaderTimeout time.Duration, allowedPrivateURLs ...string) (*http.Client, error) {
	return newPublicClient(0, responseHeaderTimeout, responseHeaderTimeout, "", allowedPrivateURLs...)
}

func NewPublicClientWithProxy(timeout time.Duration, proxyURL string) (*http.Client, error) {
	return newPublicClient(timeout, timeout, timeout, proxyURL)
}

func NewPublicStreamingClientWithProxy(responseHeaderTimeout time.Duration, proxyURL string) (*http.Client, error) {
	return newPublicClient(0, responseHeaderTimeout, responseHeaderTimeout, proxyURL)
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

func newPublicClient(timeout, responseHeaderTimeout, readTimeout time.Duration, proxyURL string, allowedPrivateURLs ...string) (*http.Client, error) {
	policy := NewPolicy(allowedPrivateURLs...)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	if proxyURL != "" {
		// Note: when a proxy is configured, the custom DialContext's SSRF
		// guard only sees the proxy host, not the ultimate target.  The
		// target URL passes through the proxy unvalidated.  This is an
		// accepted tradeoff — the proxy is assumed trusted — and should
		// be revisited if the threat model changes to include untrusted
		// proxies.
		u, err := url.Parse(proxyURL)
		if err != nil {
			return nil, fmt.Errorf("invalid proxy URL %q: %w", proxyURL, err)
		}
		transport.Proxy = http.ProxyURL(u)
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
			return policy.ValidateURL(req.URL.String())
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

func isPublicIP(ip net.IP) bool {
	return ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() &&
		!ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast() && !ip.IsUnspecified()
}

