# Chunk A: Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `--proxy` flag, shell completion, and `version` subcommand to the scraper CLI.

**Architecture:** Proxy URL flows from CLI flag or config file through `globalOptions` → `config.Config` → `netutil` client constructors. Shell completions and version are standalone Cobra subcommands.

**Tech Stack:** Go 1.26, Cobra, net/http, net/url

## Global Constraints

- P1: `--proxy` persistent flag sets an HTTP/S proxy for all outbound requests
- P2: Config file field `proxy:` sets a default; CLI flag overrides using `flags.Changed("proxy")` pattern
- P3: Invalid proxy URL fails fast before any network activity
- P4: Empty/missing proxy → no proxy (unchanged behavior)
- P5: `scraper completion [bash|zsh|fish|powershell]` prints completion script
- P6: `scraper version` prints version string, overridable at build via `-ldflags`

## Pre-Flight

- [ ] Read the current state of `internal/netutil/client.go`, `internal/config/config.go`, `cmd/root.go`
- [ ] Verify: `go build ./... && go test ./... -count=1` passes before any changes

---

### Task 1: Add proxy field to Config

**Files:**
- Modify: `internal/config/config.go:16-29` (Config struct)
- Test: `internal/config/config_test.go` (append)

**Interfaces:**
- Produces: `Config.Proxy string` with yaml tag `proxy`

- [ ] **Step 1: Add Proxy field to Config struct**

In `internal/config/config.go`, add to the `Config` struct after `SkipChecksums`:

```go
	// Proxy is the HTTP/S proxy URL for all outbound requests.
	// Empty means no proxy (default).
	Proxy string `yaml:"proxy"`
```

- [ ] **Step 2: Write config test — add TestConfigProxyRoundTrip to config_test.go**

In `internal/config/config_test.go`, append:

```go
func TestConfigProxyRoundTrip(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	content := "proxy: http://proxy.example.com:8080\n"
	if err := os.WriteFile(configPath, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Proxy != "http://proxy.example.com:8080" {
		t.Errorf("Proxy = %q, want http://proxy.example.com:8080", cfg.Proxy)
	}
}

func TestConfigProxyEmptyDefault(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Proxy != "" {
		t.Errorf("Proxy default should be empty, got %q", cfg.Proxy)
	}
}
```

The file needs `"path/filepath"` import added. Check existing imports first.

- [ ] **Step 3: Run config tests**

Run: `go test ./internal/config/... -count=1 -v -run "TestConfigProxy" -timeout=30s`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat: add Proxy field to Config struct"
```

---

### Task 2: Add proxy-aware client constructors to netutil

**Files:**
- Modify: `internal/netutil/client.go:93-99` (public constructors)
- Modify: `internal/netutil/client.go:124-141` (newPublicClient)
- Test: `internal/netutil/client_test.go` (append)

**Interfaces:**
- Produces: `netutil.NewPublicClientWithProxy(timeout time.Duration, proxyURL string) *http.Client`
- Produces: `netutil.NewPublicStreamingClientWithProxy(responseHeaderTimeout time.Duration, proxyURL string) *http.Client`

- [ ] **Step 1: Add proxy-aware constructors**

In `internal/netutil/client.go`, after line 99 (after `NewPublicStreamingClient`), add:

```go
func NewPublicClientWithProxy(timeout time.Duration, proxyURL string) *http.Client {
	return newPublicClient(timeout, timeout, timeout, proxyURL)
}

func NewPublicStreamingClientWithProxy(responseHeaderTimeout time.Duration, proxyURL string) *http.Client {
	return newPublicClient(0, responseHeaderTimeout, responseHeaderTimeout, proxyURL)
}
```

- [ ] **Step 2: Update newPublicClient signature and proxy logic**

Replace the `newPublicClient` function (lines 124-141) with:

```go
func newPublicClient(timeout, responseHeaderTimeout, readTimeout time.Duration, proxyURL string, allowedPrivateURLs ...string) *http.Client {
	policy := NewPolicy(allowedPrivateURLs...)
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	if proxyURL != "" {
		u, err := url.Parse(proxyURL)
		if err != nil {
			return &http.Client{Transport: &errorTransport{err: fmt.Errorf("invalid proxy URL %q: %w", proxyURL, err)}}
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
	}
}
```

- [ ] **Step 3: Update existing constructors to match new signature**

Replace `NewPublicClient` (line 93-95):

```go
func NewPublicClient(timeout time.Duration, allowedPrivateURLs ...string) *http.Client {
	return newPublicClient(timeout, timeout, timeout, "", allowedPrivateURLs...)
}
```

Replace `NewPublicStreamingClient` (line 97-99):

```go
func NewPublicStreamingClient(responseHeaderTimeout time.Duration, allowedPrivateURLs ...string) *http.Client {
	return newPublicClient(0, responseHeaderTimeout, responseHeaderTimeout, "", allowedPrivateURLs...)
}
```

- [ ] **Step 4: Add errorTransport type**

At the end of the file, after `isPublicIP` (line 167), add:

```go
type errorTransport struct {
	err error
}

func (t *errorTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return nil, t.err
}
```

- [ ] **Step 5: Write netutil tests**

In `internal/netutil/client_test.go`, append:

```go
func TestNewPublicClientWithProxySetsTransportProxy(t *testing.T) {
	proxyURL := "http://proxy.example.com:8080"
	client := NewPublicClientWithProxy(30*time.Second, proxyURL)
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
	client := NewPublicClientWithProxy(30*time.Second, "")
	transport := client.Transport.(*http.Transport)
	req, _ := http.NewRequest("GET", "http://example.com", nil)
	proxy, err := transport.Proxy(req)
	if err != nil {
		t.Fatal(err)
	}
	if proxy != nil {
		t.Errorf("expected no proxy for empty URL, got %v", proxy)
	}
}
```

- [ ] **Step 6: Run netutil tests**

Run: `go test ./internal/netutil/... -count=1 -v -run "TestNewPublicClientWithProxy" -timeout=30s`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/netutil/client.go internal/netutil/client_test.go
git commit -m "feat: add proxy-aware HTTP client constructors to netutil"
```

---

### Task 3: Wire proxy into downloader constructors

**Files:**
- Modify: `internal/downloader/downloader.go:60-81` (NewDownloaderWithConfig)
- Modify: `internal/parser/youtube/youtube.go:701-728` (NewDownloaderWithConfig)

**Interfaces:**
- Consumes: `config.Config.Proxy` string (Task 1)
- Consumes: `netutil.NewPublicStreamingClientWithProxy` (Task 2)

- [ ] **Step 1: Add proxy parameter to generic downloader constructor**

In `internal/downloader/downloader.go`, change `NewDownloaderWithConfig` signature (line 61):

```go
func NewDownloaderWithConfig(concurrency int, timeout time.Duration, userAgent, proxyURL string) *Downloader {
```

And update the client creation (line 72, was `netutil.NewPublicStreamingClient(timeout)`):

```go
var client *http.Client
if proxyURL != "" {
    client = netutil.NewPublicStreamingClientWithProxy(timeout, proxyURL)
} else {
    client = netutil.NewPublicStreamingClient(timeout)
}
return &Downloader{
    client:    client,
```

- [ ] **Step 2: Update call sites for generic downloader in cmd/root.go**

There are two call sites — in `runWithScraper` and `runBatch`.

In `runWithScraper` (around line 351), replace:

```go
		genericDl := downloader.NewDownloaderWithConfig(
			cfg.ConcurrentDownloads,
			cfg.Timeout,
			cfg.UserAgent,
		)
```

With:

```go
		genericDl := downloader.NewDownloaderWithConfig(
			cfg.ConcurrentDownloads,
			cfg.Timeout,
			cfg.UserAgent,
			cfg.Proxy,
		)
```

In `runBatch` (around line 540), same replacement.

- [ ] **Step 3: Wire proxy into YouTube downloader**

In `internal/parser/youtube/youtube.go`, in `NewDownloaderWithConfig` (line 701), replace the client setup line 711 (was `scraper.video.HTTPClient = netutil.WithUserAgent(netutil.NewPublicStreamingClient(config.Timeout), config.UserAgent)`):

```go
if config.ProxyURL != "" {
    scraper.video.HTTPClient = netutil.WithUserAgent(netutil.NewPublicStreamingClientWithProxy(config.Timeout, config.ProxyURL), config.UserAgent)
} else {
    scraper.video.HTTPClient = netutil.WithUserAgent(netutil.NewPublicStreamingClient(config.Timeout), config.UserAgent)
}
```

Add `ProxyURL` to `DownloaderConfig` struct (line 668-674):

```go
type DownloaderConfig struct {
	Concurrency  int
	Timeout      time.Duration
	UserAgent    string
	RateLimit    int64
	SkipChecksum bool
	ProxyURL     string
}
```

- [ ] **Step 4: Update YouTube downloader call sites**

In `cmd/root.go`, find the YouTube downloader creation (in `runWithScraper` and `runBatch`) and add `ProxyURL: cfg.Proxy`:

```go
dl = youtube.NewDownloaderWithConfig(ytScraper, youtube.DownloaderConfig{
    Concurrency:  cfg.ConcurrentDownloads,
    Timeout:      cfg.Timeout,
    UserAgent:    cfg.UserAgent,
    RateLimit:    rateLimitBytesPerSec,
    SkipChecksum: cfg.SkipChecksums,
    ProxyURL:     cfg.Proxy,
})
```

- [ ] **Step 5: Verify build**

Run: `go build ./...`
Expected: no errors

- [ ] **Step 6: Run downloader tests**

Run: `go test ./internal/downloader/... -count=1 -timeout=60s`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/downloader/downloader.go internal/parser/youtube/youtube.go cmd/root.go
git commit -m "feat: wire proxy URL into downloader constructors"
```

---

### Task 4: Add --proxy flag to CLI

**Files:**
- Modify: `cmd/root.go:31-46` (globalOptions struct)
- Modify: `cmd/root.go:102-157` (globalOptionsFromCommand)
- Modify: `cmd/root.go:203-210` (flag registration in NewRootCmd)
- Test: `cmd/root_test.go` (append)

**Interfaces:**
- Produces: `globalOptions.proxy string` and `globalOptions.proxySet bool`
- Consumes: `flags.Changed("proxy")` for override detection

- [ ] **Step 1: Add proxy fields to globalOptions**

In `cmd/root.go`, `globalOptions` struct, after `json bool`:

```go
	proxy    string
	proxySet bool
```

- [ ] **Step 2: Read proxy flag in globalOptionsFromCommand**

In `cmd/root.go`, `globalOptionsFromCommand`, after the json section (line 150):

```go
	proxy, err := flags.GetString("proxy")
	if err != nil {
		return globalOptions{}, err
	}
```

And add to the return struct:

```go
		noTui:            noTui,
		json:             json,
		proxy:            proxy,
		proxySet:         flags.Changed("proxy"),
```

- [ ] **Step 3: Apply proxy override in loadGlobalConfig**

In `cmd/root.go`, `loadGlobalConfig`, after the `SkipChecksums` override (line 64):

```go
	if opts.proxySet {
		cfg.Proxy = opts.proxy
	}
```

- [ ] **Step 4: Register --proxy flag**

In `cmd/root.go`, `NewRootCmd`, after the `--json` flag (line 209):

```go
	root.PersistentFlags().String("proxy", "", "HTTP/S proxy URL for all requests (e.g. http://proxy:8080)")
```

- [ ] **Step 5: Write CLI flag tests**

In `cmd/root_test.go`, append:

```go
func TestNewRootCmdHasProxyFlag(t *testing.T) {
	root := NewRootCmd()
	flag := root.PersistentFlags().Lookup("proxy")
	if flag == nil {
		t.Error("expected --proxy persistent flag")
	}
}

func TestLoadGlobalConfigProxyOverride(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	content := "proxy: http://config-proxy:3128\n"
	if err := os.WriteFile(configPath, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadGlobalConfig(globalOptions{
		configPath: configPath,
		proxy:      "http://cli-proxy:8080",
		proxySet:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Proxy != "http://cli-proxy:8080" {
		t.Errorf("expected CLI flag to override config, got %q", cfg.Proxy)
	}
}
```

- [ ] **Step 6: Run tests**

Run: `go test ./cmd/... -count=1 -v -run "TestNewRootCmdHasProxyFlag|TestLoadGlobalConfigProxyOverride" -timeout=30s`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add cmd/root.go cmd/root_test.go
git commit -m "feat: add --proxy flag with config file override"
```

---

### Task 5: Add version and completion subcommands

**Files:**
- Modify: `cmd/root.go:170-183` (NewRootCmd subcommand registration)
- Test: `cmd/root_test.go` (append)

**Interfaces:**
- Produces: `scraper version` subcommand
- Produces: `scraper completion [bash|zsh|fish|powershell]` subcommand

- [ ] **Step 1: Add version variable**

At the top of `cmd/root.go`, after the imports but before `globalOptions`:

```go
var version = "dev"
```

- [ ] **Step 2: Add version and completion subcommands**

In `cmd/root.go`, `NewRootCmd`, replace `root.AddCommand(...)` (lines 211-217) with:

```go
	root.AddCommand(
		newYouTubeCmd(),
		newCyberDropCmd(),
		newBunkerCmd(),
		newGoonBoxCmd(),
		newFilesterCmd(),
		&cobra.Command{
			Use:   "version",
			Short: "Print the version",
			Run: func(cmd *cobra.Command, args []string) {
				fmt.Println("scraper", version)
			},
		},
		&cobra.Command{
			Use:   "completion [bash|zsh|fish|powershell]",
			Short: "Generate shell completion script",
			Long: `To load completions:

Bash:  source <(scraper completion bash)
Zsh:   source <(scraper completion zsh)
Fish:  scraper completion fish | source`,
			Args: cobra.ExactArgs(1),
			Run: func(cmd *cobra.Command, args []string) {
				switch args[0] {
				case "bash":
					cmd.Root().GenBashCompletion(os.Stdout)
				case "zsh":
					cmd.Root().GenZshCompletion(os.Stdout)
				case "fish":
					cmd.Root().GenFishCompletion(os.Stdout, true)
				case "powershell":
					cmd.Root().GenPowerShellCompletion(os.Stdout)
				}
			},
		},
	)
```

- [ ] **Step 3: Write tests for version and completion subcommands**

In `cmd/root_test.go`, append:

```go
func TestNewRootCmdRegistersVersionAndCompletion(t *testing.T) {
	root := NewRootCmd()
	names := map[string]bool{}
	for _, c := range root.Commands() {
		names[c.Name()] = true
	}
	for _, name := range []string{"version", "completion"} {
		if !names[name] {
			t.Errorf("expected %q subcommand", name)
		}
	}
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./cmd/... -count=1 -v -run "TestNewRootCmdRegistersVersionAndCompletion" -timeout=30s`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add cmd/root.go cmd/root_test.go
git commit -m "feat: add version and shell completion subcommands"
```

---

### Task 6: Full validation

- [ ] **Step 1: Build**

Run: `go build ./...`
Expected: no errors

- [ ] **Step 2: Full test suite**

Run: `go test ./... -count=1 -timeout=180s`
Expected: all 13 packages PASS

- [ ] **Step 3: Verify --help shows new flags**

Run: `go run ./cmd/scraper --help`
Expected: output includes `--proxy`, `--json`, `--no-tui`, `--input-file`, `version`, `completion`

- [ ] **Step 4: Verify version**

Run: `go run ./cmd/scraper version`
Expected: `scraper dev`

- [ ] **Step 5: Verify completions**

Run: `go run ./cmd/scraper completion bash | head -5`
Expected: bash completion script header

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "chore: final validation for Chunk A foundation"
```
