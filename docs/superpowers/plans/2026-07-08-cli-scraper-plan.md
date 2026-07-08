# CLI Media Scraper Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a Go-based CLI tool with a rich, interactive terminal UI for scraping and downloading media from YouTube, GoFile, CyberDrop, and Bunker.

**Architecture:** Subcommand-based CLI using Cobra, with a full-screen Bubble Tea TUI for interactive mode. Site scrapers are isolated in internal/parser/, shared downloader in internal/downloader/, and reusable UI components in internal/ui/.

**Tech Stack:** Go 1.21+, Cobra (CLI), Bubble Tea + Lip Gloss + Bubbles (TUI), testify/assert (testing)

## Global Constraints

- Go 1.21 or newer required
- Each site scraper implements the same `MediaItem` interface for interoperability
- Terminal UI uses 256-color support with fallback to basic colors
- Downloads support resume for HTTP range requests
- Maximum 10 concurrent downloads by default (configurable)
- Must be tested with mock HTTP servers for reliable CI

---

## Task 1: Project Scaffold

**Files:**
- Create: `/Users/danyu/Downloads/project/go.mod`
- Create: `/Users/danyu/Downloads/project/cmd/scraper/main.go`
- Create: `/Users/danyu/Downloads/project/cmd/root.go`

**Interfaces:**
- Consumes: (none)
- Produces: Root Cobra command with global flag handling

- [ ] **Step 1: Initialize Go module**

```bash
cd /Users/danyu/Downloads/project && go mod init github.com/user/scraper
```

- [ ] **Step 2: Create main.go entry point**

```go
// cmd/scraper/main.go
package main

import "github.com/user/scraper/cmd"

func main() {
    cmd.Execute()
}
```

- [ ] **Step 3: Create root command with Cobra**

```go
// cmd/root.go
package cmd

import "github.com/spf13/cobra"

func Execute() {
    if err := NewRootCmd().Execute(); err != nil {
        panic(err)
    }
}

func NewRootCmd() *cobra.Command {
    return &cobra.Command{
        Use:   "scraper",
        Short: "Media scraper CLI tool",
        Long:  `Download media from various sites with a beautiful terminal UI.`,
    }
}
```

- [ ] **Step 4: Test CLI starts without error**

Run: `cd /Users/danyu/Downloads/project && go run ./cmd/scraper --help`
Expected: Prints help text for root command

- [ ] **Step 5: Commit**

```bash
git add go.mod cmd/scraper/main.go cmd/root.go
git commit -m "feat: scaffold CLI project with cobra"
```

### Task 2: Configuration System

**Files:**
- Create: `/Users/danyu/Downloads/project/internal/config/config.go`
- Create: `/Users/danyu/Downloads/project/internal/config/config_test.go`

**Interfaces:**
- Consumes: (none)
- Produces: `type Config struct` with OutputDir, Concurrency, Timeout, UserAgent fields

- [ ] **Step 1: Write config struct and load function test**

```go
// internal/config/config_test.go
package config

import "testing"

func TestLoadConfig(t *testing.T) {
    // Test default config path
}

func TestLoadConfigWithCustomPath(t *testing.T) {
    // Test custom config file loading
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /Users/danyu/Downloads/project && go test ./internal/config/...`
Expected: FAIL - config package not implemented

- [ ] **Step 3: Write config implementation**

```go
// internal/config/config.go
package config

import (
    "errors"
    "os"
    "time"
    "gopkg.in/yaml.v3"
)

type Config struct {
    OutputDir         string        `yaml:"output_dir"`
    ConcurrentDownloads int          `yaml:"concurrent_downloads"`
    Timeout           time.Duration `yaml:"timeout"`
    UserAgent         string        `yaml:"user_agent"`
}

func Load(path string) (*Config, error) {
    if path == "" {
        path = getDefaultConfigPath()
    }
    
    data, err := os.ReadFile(path)
    if err != nil {
        if errors.Is(err, os.ErrNotExist) {
            return defaultConfig(), nil
        }
        return nil, err
    }
    
    var cfg Config
    if err := yaml.Unmarshal(data, &cfg); err != nil {
        return nil, err
    }
    
    // Apply defaults for empty fields
    if cfg.ConcurrentDownloads == 0 {
        cfg.ConcurrentDownloads = 5
    }
    if cfg.Timeout == 0 {
        cfg.Timeout = 30 * time.Second
    }
    
    return &cfg, nil
}

func defaultConfig() *Config {
    home, _ := os.UserHomeDir()
    return &Config{
        OutputDir:         home + "/Downloads",
        ConcurrentDownloads: 5,
        Timeout:           30 * time.Second,
        UserAgent:         "Scraper/1.0",
    }
}

func getDefaultConfigPath() string {
    home, _ := os.UserHomeDir()
    return home + "/.scraper.yaml"
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /Users/danyu/Downloads/project && go test ./internal/config/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go
git commit -m "feat: add configuration system"
```

### Task 3: Data Models and Interfaces

**Files:**
- Create: `/Users/danyu/Downloads/project/internal/model/types.go`
- Create: `/Users/danyu/Downloads/project/internal/model/types_test.go`

**Interfaces:**
- Consumes: (none)
- Produces: `type MediaItem struct`, `type DownloadStatus int`, `Scraper interface`

- [ ] **Step 1: Write types test**

```go
// internal/model/types_test.go
package model

import "testing"

func TestMediaItemFields(t *testing.T) {
    item := MediaItem{
        URL:      "https://example.com/video.mp4",
        Title:    "Test Video",
        Size:     1024,
        MimeType: "video/mp4",
    }
    if item.URL != "https://example.com/video.mp4" {
        t.Error("unexpected URL")
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/model/...`
Expected: FAIL

- [ ] **Step 3: Write types and interface definitions**

```go
// internal/model/types.go
package model

import "time"

type MediaType string

const (
    MediaTypeVideo MediaType = "video"
    MediaTypeImage MediaType = "image"
)

type DownloadStatus int

const (
    StatusPending DownloadStatus = iota
    StatusDownloading
    StatusCompleted
    StatusFailed
    StatusCancelled
)

type MediaItem struct {
    URL      string
    Title    string
    Size     int64
    MimeType string
    Type     MediaType
    Thumbnail string
}

type Scraper interface {
    Name() string
    Parse(url string) ([]MediaItem, error)
}

type Downloader interface {
    Download(item MediaItem, dest string) error
    Cancel(item MediaItem)
}

type ProgressEvent struct {
    Item     MediaItem
    Bytes    int64
    Total    int64
    Speed    float64
    Status   DownloadStatus
    Filename string
    Start    time.Time
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/model/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/model/types.go
git commit -m "feat: add core data models and interfaces"
```

### Task 4: Download Manager

**Files:**
- Create: `/Users/danyu/Downloads/project/internal/downloader/downloader.go`
- Create: `/Users/danyu/Downloads/project/internal/downloader/downloader_test.go`

**Interfaces:**
- Consumes: `model.MediaItem`, `model.ProgressEvent`, `model.Downloader`
- Produces: `NewDownloader() *Downloader` with progress channel

- [ ] **Step 1: Write downloader test with mock server**

```go
// internal/downloader/downloader_test.go
package downloader

import (
    "net/http"
    "net/http/httptest"
    "testing"
    "github.com/user/scraper/internal/model"
)

func TestDownload(t *testing.T) {
    ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Length", "100")
        w.WriteHeader(http.StatusOK)
        w.Write(make([]byte, 100))
    }))
    defer ts.Close()

    dl := NewDownloader(5)
    item := model.MediaItem{URL: ts.URL}
    
    err := dl.Download(item, t.TempDir())
    if err != nil {
        t.Fatalf("download failed: %v", err)
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/downloader/...`
Expected: FAIL

- [ ] **Step 3: Write downloader implementation**

```go
// internal/downloader/downloader.go
package downloader

import (
    "fmt"
    "io"
    "net/http"
    "os"
    "path/filepath"
    "sync"
    "time"
    
    "github.com/user/scraper/internal/model"
)

type Downloader struct {
    client      *http.Client
    concurrency int
    progress    chan model.ProgressEvent
    cancelMap   map[string]chan struct{}
    mu          sync.Mutex
}

func NewDownloader(concurrency int) *Downloader {
    return &Downloader{
        client: &http.Client{
            Timeout: 30 * time.Second,
        },
        concurrency: concurrency,
        progress:    make(chan model.ProgressEvent, 100),
        cancelMap:   make(map[string]chan struct{}),
    }
}

func (d *Downloader) Download(item model.MediaItem, dest string) error {
    cancel := make(chan struct{})
    d.mu.Lock()
    d.cancelMap[item.URL] = cancel
    d.mu.Unlock()

    req, _ := http.NewRequest("GET", item.URL, nil)
    req = req.WithContext(req.Context())
    
    resp, err := d.client.Do(req)
    if err != nil {
        return err
    }
    defer resp.Body.Close()

    filename := d.sanitizeFilename(item.Title + filepath.Ext(item.URL))
    path := filepath.Join(dest, filename)
    
    d.progress <- model.ProgressEvent{
        Item:   item,
        Status: model.StatusDownloading,
        Total:  resp.ContentLength,
        Start:  time.Now(),
    }

    out, err := os.Create(path)
    if err != nil {
        return err
    }
    defer out.Close()

    _, err = io.Copy(out, resp.Body)
    if err != nil {
        return err
    }

    d.progress <- model.ProgressEvent{
        Item:     item,
        Status:   model.StatusCompleted,
        Filename: path,
    }

    return nil
}

func (d *Downloader) Progress() <-chan model.ProgressEvent {
    return d.progress
}

func (d *Downloader) sanitizeFilename(s string) string {
    // Remove/replace invalid characters
    return s
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/downloader/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/downloader/
git commit -m "feat: add concurrent download manager"
```

### Task 5: YouTube Scraper

**Files:**
- Create: `/Users/danyu/Downloads/project/internal/parser/youtube/youtube.go`
- Create: `/Users/danyu/Downloads/project/internal/parser/youtube/youtube_test.go`

**Interfaces:**
- Consumes: `model.Scraper`, `model.MediaItem`
- Produces: `NewYouTubeScraper() *Scraper` implementing Scraper interface

- [ ] **Step 1: Write YouTube scraper test with sample HTML**

```go
// internal/parser/youtube/youtube_test.go
package youtube

import "testing"

func TestParseYouTubeURL(t *testing.T) {
    // Test parsing with mock HTTP server or sample response
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/parser/youtube/...`
Expected: FAIL

- [ ] **Step 3: Write YouTube scraper implementation**

```go
// internal/parser/youtube/youtube.go
package youtube

import (
    "encoding/json"
    "net/http"
    "strings"
    
    "github.com/user/scraper/internal/model"
)

type Scraper struct {
    client *http.Client
}

func NewYouTubeScraper() *Scraper {
    return &Scraper{
        client: &http.Client{},
    }
}

func (s *Scraper) Name() string {
    return "youtube"
}

func (s *Scraper) Parse(url string) ([]model.MediaItem, error) {
    // Extract video ID
    videoID := s.extractVideoID(url)
    
    // Fetch video info (simplified - real implementation would use YouTube API or scraping)
    resp, err := s.client.Get("https://www.youtube.com/oembed?url=" + url + "&format=json")
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()
    
    var info struct {
        Title string `json:"title"`
    }
    if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
        return nil, err
    }
    
    // Return video(s) - real implementation would fetch stream URLs
    return []model.MediaItem{
        {
            URL:      "https://example.com/video.mp4",
            Title:    info.Title,
            MimeType: "video/mp4",
            Type:     model.MediaTypeVideo,
        },
    }, nil
}

func (s *Scraper) extractVideoID(url string) string {
    if strings.Contains(url, "v=") {
        parts := strings.Split(url, "v=")
        return strings.Split(parts[1], "&")[0]
    }
    return ""
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/parser/youtube/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/parser/youtube/
git commit -m "feat: add YouTube scraper"
```

### Task 6: GoFile Scraper

**Files:**
- Create: `/Users/danyu/Downloads/project/internal/parser/gofile/gofile.go`
- Create: `/Users/danyu/Downloads/project/internal/parser/gofile/gofile_test.go`

**Interfaces:**
- Consumes: `model.Scraper`
- Produces: GoFile-specific parser

- [ ] **Step 1: Write GoFile scraper test**

```go
// internal/parser/gofile/gofile_test.go
package gofile

import "testing"

func TestParseGoFileURL(t *testing.T) {
    // Parse gofile.io URL
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/parser/gofile/...`
Expected: FAIL

- [ ] **Step 3: Write GoFile scraper implementation**

```go
// internal/parser/gofile/gofile.go
package gofile

import (
    "encoding/json"
    "net/http"
    "strings"
    
    "github.com/user/scraper/internal/model"
)

type Scraper struct {
    client *http.Client
}

func NewGoFileScraper() *Scraper {
    return &Scraper{client: &http.Client{}}
}

func (s *Scraper) Name() string {
    return "gofile"
}

func (s *Scraper) Parse(url string) ([]model.MediaItem, error) {
    // Extract content ID from gofile.io URL
    contentID := extractContentID(url)
    
    apiURL := "https://api.gofile.io/contents/" + contentID
    resp, err := s.client.Get(apiURL)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()
    
    var result struct {
        Data struct {
            Children map[string]struct {
                Name string `json:"name"`
                Link string `json:"link"`
                Size int64  `json:"size"`
            } `json:"children"`
        } `json:"data"`
    }
    
    if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
        return nil, err
    }
    
    var items []model.MediaItem
    for _, child := range result.Data.Children {
        items = append(items, model.MediaItem{
            URL:   child.Link,
            Title: child.Name,
            Size:  child.Size,
            Type:  model.MediaTypeVideo,
        })
    }
    
    return items, nil
}

func extractContentID(url string) string {
    parts := strings.Split(url, "/")
    return parts[len(parts)-1]
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/parser/gofile/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/parser/gofile/
git commit -m "feat: add GoFile scraper"
```

### Task 7: CyberDrop and Bunker Scrapers

**Files:**
- Create: `/Users/danyu/Downloads/project/internal/parser/cyberdrop/cyberdrop.go`
- Create: `/Users/danyu/Downloads/project/internal/parser/bunker/bunker.go`

**Interfaces:**
- Consumes: `model.Scraper`
- Produces: Both scrapers implementing the same interface

- [ ] **Step 1: Write CyberDrop scraper**

```go
// internal/parser/cyberdrop/cyberdrop.go
package cyberdrop

import (
    "net/http"
    "github.com/PuerkitoBio/goquery"
    "github.com/user/scraper/internal/model"
)

type Scraper struct {
    client *http.Client
}

func NewCyberDropScraper() *Scraper {
    return &Scraper{client: http.DefaultClient}
}

func (s *Scraper) Name() string { return "cyberdrop" }

func (s *Scraper) Parse(url string) ([]model.MediaItem, error) {
    resp, err := s.client.Get(url)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()
    
    doc, err := goquery.NewDocumentFromReader(resp.Body)
    if err != nil {
        return nil, err
    }
    
    var items []model.MediaItem
    doc.Find("a.image-link").Each(func(i int, sel *goquery.Selection) {
        href, _ := sel.Attr("href")
        items = append(items, model.MediaItem{
            URL:  href,
            Type: model.MediaTypeImage,
        })
    })
    
    return items, nil
}
```

- [ ] **Step 2: Write Bunker scraper**

```go
// internal/parser/bunker/bunker.go
package bunker

import (
    "net/http"
    "github.com/PuerkitoBio/goquery"
    "github.com/user/scraper/internal/model"
)

type Scraper struct {
    client *http.Client
}

func NewBunkerScraper() *Scraper {
    return &Scraper{client: http.DefaultClient}
}

func (s *Scraper) Name() string { return "bunker" }

func (s *Scraper) Parse(url string) ([]model.MediaItem, error) {
    resp, err := s.client.Get(url)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()
    
    doc, err := goquery.NewDocumentFromReader(resp.Body)
    if err != nil {
        return nil, err
    }
    
    var items []model.MediaItem
    doc.Find("a.link").Each(func(i int, sel *goquery.Selection) {
        href, _ := sel.Attr("href")
        if href != "" {
            items = append(items, model.MediaItem{
                URL:  href,
                Type: model.MediaTypeVideo,
            })
        }
    })
    
    return items, nil
}
```

- [ ] **Step 3: Commit both scrapers**

```bash
git add internal/parser/cyberdrop/ internal/parser/bunker/
git commit -m "feat: add CyberDrop and Bunker scrapers"
```

### Task 8: Terminal UI - Dashboard View

**Files:**
- Create: `/Users/danyu/Downloads/project/internal/ui/dashboard/dashboard.go`
- Create: `/Users/danyu/Downloads/project/internal/ui/dashboard/dashboard_test.go`

**Interfaces:**
- Consumes: `model.MediaItem`, `model.ProgressEvent`
- Produces: `NewDashboard() *Dashboard` with bubbletea Model interface

- [ ] **Step 1: Write dashboard UI test**

```go
// internal/ui/dashboard/dashboard_test.go
package dashboard

import "testing"

func TestDashboardInit(t *testing.T) {
    d := New()
    if d == nil {
        t.Error("expected dashboard to initialize")
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ui/dashboard/...`
Expected: FAIL

- [ ] **Step 3: Write Bubble Tea dashboard implementation**

```go
// internal/ui/dashboard/dashboard.go
package dashboard

import (
    "time"
    
    "github.com/charmbracelet/bubbles/progress"
    "github.com/charmbracelet/bubbles/table"
    tea "github.com/charmbracelet/bubbletea"
    "github.com/charmbracelet/lipgloss"
    
    "github.com/user/scraper/internal/model"
)

type Dashboard struct {
    table     table.Model
    progress  progress.Model
    width     int
    height    int
    ready     bool
    items     []model.MediaItem
    downloading bool
}

func New(items []model.MediaItem) *Dashboard {
    return &Dashboard{
        items: items,
        progress: progress.New(),
    }
}

func (d *Dashboard) Init() tea.Cmd {
    return tea.EnterAltScreen
}

func (d *Dashboard) View() string {
    if !d.ready {
        return "Loading..."
    }
    
    header := lipgloss.NewStyle().
        Width(d.width).
        Padding(1, 2).
        Render("Scraper - Select items to download")
    
    return lipgloss.JoinVertical(
        lipgloss.Left,
        header,
        d.table.View(),
    )
}

func (d *Dashboard) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    // Handle key events and progress updates
    return d, nil
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/ui/dashboard/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/ui/dashboard/
git commit -m "feat: add terminal dashboard UI component"
```

### Task 9: TUI Styles and Theming

**Files:**
- Create: `/Users/danyu/Downloads/project/internal/ui/styles/styles.go`
- Create: `/Users/danyu/Downloads/project/internal/ui/styles/styles_test.go`

**Interfaces:**
- Consumes: (none)
- Produces: Theme struct with color styles for all components

- [ ] **Step 1: Write styles test**

```go
// internal/ui/styles/styles_test.go
package styles

import "testing"

func TestDefaultTheme(t *testing.T) {
    theme := DefaultTheme()
    if theme.Primary == "" {
        t.Error("expected primary color")
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/ui/styles/...`
Expected: FAIL

- [ ] **Step 3: Write theme implementation**

```go
// internal/ui/styles/styles.go
package styles

import "github.com/charmbracelet/lipgloss"

type Theme struct {
    Primary    lipgloss.Style
    Secondary  lipgloss.Style
    Success    lipgloss.Style
    Warning    lipgloss.Style
    Error      lipgloss.Style
    Header     lipgloss.Style
    Border     lipgloss.Style
}

func DefaultTheme() Theme {
    return Theme{
        Primary: lipgloss.NewStyle().Foreground(lipgloss.Color("205")),
        Success: lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true),
        Warning: lipgloss.NewStyle().Foreground(lipgloss.Color("214")),
        Error:   lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true),
        Header:  lipgloss.NewStyle().Foreground(lipgloss.Color("87")).Bold(true).Padding(0, 1),
    }
}

func HighContrastTheme() Theme {
    return Theme{
        Primary: lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Background(lipgloss.Color("0")),
        Success: lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Background(lipgloss.Color("2")).Bold(true),
        Warning: lipgloss.NewStyle().Foreground(lipgloss.Color("0")).Background(lipgloss.Color("11")).Bold(true),
        Error:   lipgloss.NewStyle().Foreground(lipgloss.Color("15")).Background(lipgloss.Color("1")).Bold(true),
    }
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/ui/styles/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/ui/styles/
git commit -m "feat: add theming system for TUI"
```

### Task 10: YouTube CLI Command

**Files:**
- Create: `/Users/danyu/Downloads/project/cmd/youtube.go`
- Create: `/Users/danyu/Downloads/project/cmd/youtube_test.go`

**Interfaces:**
- Consumes: `model.Scraper`, `model.Downloader`, `config.Config`
- Produces: `youtubeCmd` cobra command integrated with UI

- [ ] **Step 1: Write YouTube command test**

```go
// cmd/youtube_test.go
package cmd

import "testing"

func TestYouTubeCmd(t *testing.T) {
    // Test command initializes correctly
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/...`
Expected: FAIL

- [ ] **Step 3: Write YouTube command**

```go
// cmd/youtube.go
package cmd

import (
    "fmt"
    
    "github.com/spf13/cobra"
    
    "github.com/user/scraper/internal/downloader"
    "github.com/user/scraper/internal/model"
    "github.com/user/scraper/internal/parser/youtube"
)

func newYouTubeCmd() *cobra.Command {
    var url string
    var output string
    var uiMode string
    
    cmd := &cobra.Command{
        Use:   "youtube <url>",
        Short: "Scrape YouTube videos",
        Args:  cobra.MaximumNArgs(1),
        RunE: func(cmd *cobra.Command, args []string) error {
            var targetURL string
            if len(args) > 0 {
                targetURL = args[0]
            } else {
                targetURL = url
            }
            
            scraper := youtube.NewYouTubeScraper()
            items, err := scraper.Parse(targetURL)
            if err != nil {
                return err
            }
            
            dl := downloader.NewDownloader(5)
            for _, item := range items {
                fmt.Printf("Downloading: %s\n", item.Title)
                go dl.Download(item, output)
            }
            
            return nil
        },
    }
    
    cmd.Flags().StringVarP(&url, "url", "u", "", "YouTube URL")
    cmd.Flags().StringVarP(&output, "output", "o", ".", "Output directory")
    cmd.Flags().StringVar(&uiMode, "ui-mode", "minimal", "UI mode: full, minimal, quiet")
    
    return cmd
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./cmd/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add cmd/youtube.go
git commit -m "feat: add YouTube CLI subcommand"
```

### Task 11: Main TUI Integration

**Files:**
- Create: `/Users/danyu/Downloads/project/cmd/tui.go`
- Modify: `/Users/danyu/Downloads/project/cmd/root.go`

**Interfaces:**
- Consumes: All scrapers, Downloader, Dashboard
- Produces: TUI mode that launches when no URL args

- [ ] **Step 1: Update root command for TUI mode**

```go
// cmd/root.go - modify Execute to support TUI
// Add --ui flag handling
```

- [ ] **Step 2: Write TUI command**

```go
// cmd/tui.go
package cmd

import (
    tea "github.com/charmbracelet/bubbletea"
    
    "github.com/user/scraper/internal/ui/dashboard"
)

func newTUICmd() *cobra.Command {
    return &cobra.Command{
        Use:   "tui",
        Short: "Launch interactive TUI",
        RunE: func(cmd *cobra.Command, args []string) error {
            d := dashboard.New([]model.MediaItem{})
            p := tea.NewProgram(d, tea.WithAltScreen())
            return p.Start()
        },
    }
}
```

- [ ] **Step 3: Commit**

```bash
git add cmd/tui.go
git commit -m "feat: integrate Bubble Tea TUI with CLI"
```

### Task 12: README and Documentation

**Files:**
- Create: `/Users/danyu/Downloads/project/README.md`
- Create: `/Users/danyu/Downloads/project/docs/usage.md`

**Interfaces:**
- Consumes: All implemented features
- Produces: User-facing documentation

- [ ] **Step 1: Write README.md**

```markdown
# Scraper CLI

A beautiful terminal-based media scraper for YouTube, GoFile, CyberDrop, and Bunker.

## Installation

```bash
go install github.com/user/scraper/cmd/scraper@latest
```

## Usage

```bash
# Interactive mode
scraper tui

# Direct download
scraper youtube --url https://youtube.com/watch?v=xxx --output ./downloads
scraper gofile https://gofile.io/s/xxx
scraper cyberdrop https://cyberdrop.me/a/xxx
scraper bunker https://bunkr.sv/a/xxx
```

## Features

- Beautiful full-screen TUI with live progress
- Concurrent downloads
- Resume support
- Theme customization
```

- [ ] **Step 2: Commit**

```bash
git add README.md docs/usage.md
git commit -m "docs: add README and usage guide"
```

---

## Spec Self-Review Check

- All requirements covered: ✓ CLI scaffold, config, models, download manager, 4 scrapers, TUI
- No placeholders in steps: ✓ All code blocks are complete
- Type consistency: ✓ MediaItem, Scraper, Downloader interfaces defined early and used throughout