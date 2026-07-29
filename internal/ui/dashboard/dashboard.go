// internal/ui/dashboard/dashboard.go
package dashboard

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/user/scraper/internal/model"
	"github.com/user/scraper/internal/ui/styles"
)

type Downloader interface {
	Download(item model.MediaItem, dest string) error
	Progress() <-chan model.ProgressEvent
}

type YouTubeDownloader interface {
	DownloadVideoWithQuality(videoID, dest string, height int) error
}

type phase int

const (
	phaseLoading phase = iota
	phaseSelect
	phaseDownload
	phaseDone
)

type itemState struct {
	item    model.MediaItem
	status  model.DownloadStatus
	bytes   int64
	total   int64
	speed   float64
	started time.Time
}

type Dashboard struct {
	table    table.Model
	progress progress.Model
	spinner  spinner.Model
	theme    styles.Theme

	states     []itemState
	downloader Downloader
	dest       string

	width  int
	height int
	ready  bool

	quitting bool
	status   string
	site     string
	phase    phase

	scraperFn  func() ([]model.MediaItem, error)
	loadingErr string

	ytDownloader YouTubeDownloader
	ytVideoID    string
	ytTitle      string
	ytBytes      int64
	ytTotal      int64
	ytSpeed      float64
	ytStarted    time.Time
	ytFailed     bool
	sendFunc     func(tea.Msg)
	albumSlug    string
}

// Futuristic logo — clean, minimal
const logo = "◢◤  SCRAPER  ◢◤"

func New(scraperFn func() ([]model.MediaItem, error), dl Downloader, dest string, site string) *Dashboard {
	isYouTube := site == "youtube"

	var columns []table.Column
	if isYouTube {
		columns = []table.Column{
			{Title: "", Width: 2},
			{Title: "QUALITY", Width: 12},
			{Title: "SIZE", Width: 10},
			{Title: "NOTE", Width: 30},
		}
	} else {
		columns = []table.Column{
			{Title: "", Width: 2},
			{Title: "FILE", Width: 42},
			{Title: "STATUS", Width: 12},
			{Title: "PROGRESS", Width: 16},
		}
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithFocused(true),
		table.WithHeight(4),
	)

	sp := spinner.New()
	sp.Spinner = spinner.MiniDot
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color(styles.ColorCyan))

	d := &Dashboard{
		table:      t,
		progress:   progress.New(progress.WithSolidFill(styles.ColorAccent)),
		spinner:    sp,
		theme:      styles.DefaultTheme(),
		downloader: dl,
		dest:       dest,
		site:       site,
		phase:      phaseLoading,
		scraperFn:  scraperFn,
	}
	return d
}

func (d *Dashboard) SetYouTubeDownloader(yd YouTubeDownloader, videoID string) {
	d.ytDownloader = yd
	d.ytVideoID = videoID
}

func (d *Dashboard) SetSendFunc(send func(tea.Msg)) {
	d.sendFunc = send
}

// SetAlbumSlug stores the URL slug for creating album subfolders.
func (d *Dashboard) SetAlbumSlug(slug string) {
	d.albumSlug = slug
}

func (d *Dashboard) Init() tea.Cmd {
	return tea.Batch(tea.EnterAltScreen, d.spinner.Tick, d.startScrape(),
		tea.Tick(500*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg(t) }))
}

// --- Messages ---

type progressMsg model.ProgressEvent
type downloadDoneMsg struct {
	url string
	err error
}
type youtubeDoneMsg struct{ err error }
type tickMsg time.Time
type scrapeResultMsg struct {
	items []model.MediaItem
	err   error
}

func (d *Dashboard) startScrape() tea.Cmd {
	return func() tea.Msg {
		items, err := d.scraperFn()
		return scrapeResultMsg{items: items, err: err}
	}
}

func startDownload(dl Downloader, item model.MediaItem, dest string) tea.Cmd {
	return func() tea.Msg {
		err := dl.Download(item, dest)
		return downloadDoneMsg{url: item.URL, err: err}
	}
}

// --- Update ---

func (d *Dashboard) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		d.width, d.height, d.ready = msg.Width, msg.Height, true
		d.table.SetWidth(msg.Width - 6)
		bw := msg.Width - 8
		if bw > 80 {
			bw = 80
		}
		if bw > 0 {
			d.progress.Width = bw
		}
		return d, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		d.spinner, cmd = d.spinner.Update(msg)
		return d, cmd

	case tickMsg:
		// Recalculate speed from elapsed time on each tick
		for i := range d.states {
			if d.states[i].status == model.StatusDownloading && !d.states[i].started.IsZero() {
				elapsed := time.Since(d.states[i].started).Seconds()
				if elapsed > 0 && d.states[i].bytes > 0 {
					d.states[i].speed = float64(d.states[i].bytes) / elapsed
				}
			}
		}
		d.refreshRows()
		return d, tea.Tick(500*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg(t) })

	case scrapeResultMsg:
		return d.handleScrapeResult(msg)

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			d.quitting = true
			return d, tea.Quit
		case "up", "k":
			d.table.MoveUp(1)
			return d, nil
		case "down", "j":
			d.table.MoveDown(1)
			return d, nil
		case "enter":
			if d.phase == phaseSelect && d.ytDownloader != nil {
				return d, d.startYouTubeDownload()
			}
			return d, nil
		}

	case progressMsg:
		evt := model.ProgressEvent(msg)
		if d.ytDownloader != nil && d.phase == phaseDownload {
			d.ytBytes = evt.Bytes
			if evt.Total > 0 {
				d.ytTotal = evt.Total
			}
			if !d.ytStarted.IsZero() {
				elapsed := time.Since(d.ytStarted).Seconds()
				if elapsed > 0 && evt.Bytes > 0 {
					d.ytSpeed = float64(evt.Bytes) / elapsed
				}
			}
		} else {
			d.applyProgress(evt)
			d.refreshRows()
		}
		return d, nil

	case downloadDoneMsg:
		d.applyDone(msg)
		d.refreshRows()
		return d, nil

	case youtubeDoneMsg:
		if msg.err != nil {
			d.ytFailed = true
			d.status = fmt.Sprintf("✗ %v", msg.err)
		} else {
			d.ytBytes = d.ytTotal
			d.status = "✓ download complete"
		}
		d.phase = phaseDone
		return d, nil
	}

	var cmd tea.Cmd
	d.table, cmd = d.table.Update(msg)
	return d, cmd
}

func (d *Dashboard) handleScrapeResult(msg scrapeResultMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil && len(msg.items) == 0 {
		d.loadingErr = msg.err.Error()
		d.phase = phaseDone
		return d, nil
	}
	if len(msg.items) == 0 {
		d.loadingErr = "no media items found"
		d.phase = phaseDone
		return d, nil
	}

	d.states = make([]itemState, len(msg.items))
	for i, it := range msg.items {
		d.states[i] = itemState{item: it, status: model.StatusPending, total: it.Size}
	}

	// Album (multiple files): create a subfolder from the URL slug
	if len(msg.items) > 1 && d.albumSlug != "" {
		d.dest = filepath.Join(d.dest, d.albumSlug)
		os.MkdirAll(d.dest, 0755)
	}

	h := len(msg.items)
	if h > 15 {
		h = 15
	}
	if h < 4 {
		h = 4
	}
	d.table.SetHeight(h)

	if d.site == "youtube" {
		d.phase = phaseSelect
	} else {
		d.phase = phaseDownload
	}
	d.refreshRows()

	var cmds []tea.Cmd
	if d.site != "youtube" && d.downloader != nil {
		// Start a background goroutine that continuously drains the progress
		// channel and sends each event to the tea program. This avoids
		// the re-arm pattern that starves downloadDoneMsg processing.
		go d.drainProgress()
		cmds = append(cmds, d.startAllDownloads())
	}
	return d, tea.Batch(cmds...)
}

// drainProgress reads from the downloader's progress channel in a loop
// and sends each event to the tea program via sendFunc. This runs for
// the lifetime of the dashboard and doesn't block the event loop.
func (d *Dashboard) drainProgress() {
	if d.downloader == nil || d.sendFunc == nil {
		return
	}
	ch := d.downloader.Progress()
	for evt := range ch {
		d.sendFunc(progressMsg(evt))
	}
}

// --- YouTube ---

func (d *Dashboard) startYouTubeDownload() tea.Cmd {
	idx := d.table.Cursor()
	if idx < 0 || idx >= len(d.states) {
		return nil
	}
	height := d.parseHeightFromTitle(d.states[idx].item.Title)
	d.ytTitle = d.extractTitle(d.states[idx].item.Title)
	d.ytStarted = time.Now()
	d.ytBytes = 0
	d.ytTotal = d.states[idx].item.Size
	d.phase = phaseDownload

	return func() tea.Msg {
		if yt, ok := d.ytDownloader.(interface{ SetProgressFunc(func(int64, int64)) }); ok {
			yt.SetProgressFunc(func(bytes, total int64) {
				if d.sendFunc != nil {
					d.sendFunc(progressMsg(model.ProgressEvent{
						Bytes: bytes, Total: total, Status: model.StatusDownloading,
					}))
				}
			})
		}
		err := d.ytDownloader.DownloadVideoWithQuality(d.ytVideoID, d.dest, height)
		return youtubeDoneMsg{err: err}
	}
}

func (d *Dashboard) parseHeightFromTitle(title string) int {
	idx := strings.Index(title, "[")
	if idx < 0 {
		return 0
	}
	end := strings.Index(title[idx:], "p]")
	if end < 0 {
		return 0
	}
	var h int
	fmt.Sscanf(title[idx+1:idx+end], "%d", &h)
	return h
}

func (d *Dashboard) extractTitle(title string) string {
	if idx := strings.Index(title, " ["); idx >= 0 {
		return title[:idx]
	}
	return title
}

// --- Batch ---

func (d *Dashboard) startAllDownloads() tea.Cmd {
	if d.downloader == nil {
		return nil
	}
	var cmds []tea.Cmd
	for i := range d.states {
		if d.states[i].item.URL == "" {
			continue
		}
		d.states[i].status = model.StatusDownloading
		d.states[i].started = time.Now()
		cmds = append(cmds, startDownload(d.downloader, d.states[i].item, d.dest))
	}
	d.refreshRows()
	return tea.Batch(cmds...)
}

func (d *Dashboard) applyProgress(evt model.ProgressEvent) {
	for i := range d.states {
		if d.states[i].item.URL != evt.Item.URL {
			continue
		}
		if d.states[i].started.IsZero() {
			d.states[i].started = time.Now()
		}
		elapsed := time.Since(d.states[i].started).Seconds()
		d.states[i].status = evt.Status
		d.states[i].bytes = evt.Bytes
		if evt.Total > 0 {
			d.states[i].total = evt.Total
		}
		if elapsed > 0 && evt.Bytes > 0 {
			d.states[i].speed = float64(evt.Bytes) / elapsed
		}
		return
	}
}

func (d *Dashboard) applyDone(msg downloadDoneMsg) {
	for i := range d.states {
		if d.states[i].item.URL != msg.url {
			continue
		}
		if msg.err != nil {
			d.states[i].status = model.StatusFailed
		} else {
			d.states[i].status = model.StatusCompleted
			if d.states[i].total > 0 {
				d.states[i].bytes = d.states[i].total
			}
		}
		break
	}
}

// --- Rendering ---

func (d *Dashboard) refreshRows() {
	rows := make([]table.Row, len(d.states))
	for i, st := range d.states {
		mark := " "
		switch st.status {
		case model.StatusDownloading:
			mark = d.spinner.View()
		case model.StatusCompleted:
			mark = "✓"
		case model.StatusFailed:
			mark = "✗"
		case model.StatusPending:
			mark = "·"
		}

		if d.site == "youtube" {
			q, note := d.splitQualityNote(st.item.Title)
			rows[i] = table.Row{mark, q, humanizeBytes(st.total), note}
		} else {
			rows[i] = table.Row{mark, truncate(st.item.Title, 40), statusShort(st.status), miniBar(st)}
		}
	}
	d.table.SetRows(rows)
}

func (d *Dashboard) splitQualityNote(title string) (quality, note string) {
	sIdx := strings.Index(title, " [")
	if sIdx < 0 {
		return title, ""
	}
	rest := title[sIdx+1:]
	quality = rest
	pIdx := strings.Index(rest, " (")
	if pIdx >= 0 {
		quality = rest[:pIdx]
		note = rest[pIdx+1 : len(rest)-1]
	}
	return quality, note
}

// --- View ---

func (d *Dashboard) View() string {
	if d.quitting {
		return ""
	}

	logo := d.theme.Cyan.Bold(true).Render(logo)
	site := d.theme.Dim.Render(fmt.Sprintf(" %s → %s", d.site, d.dest))
	header := lipgloss.JoinVertical(lipgloss.Left, logo, site)

	if d.phase == phaseLoading {
		return d.viewLoading(header)
	}
	if d.phase == phaseDone && d.loadingErr != "" && len(d.states) == 0 {
		return d.viewError(header)
	}
	if d.ytDownloader != nil {
		return d.viewYouTube(header)
	}
	return d.viewBatch(header)
}

func (d *Dashboard) viewLoading(header string) string {
	msg := d.spinner.View() + " " + d.theme.Dim.Render("fetching media items...")
	return lipgloss.JoinVertical(lipgloss.Left,
		"", header, "",
		d.theme.PanelSlim.Render(msg), "",
		d.theme.Footer.Render("  q quit"),
	)
}

func (d *Dashboard) viewError(header string) string {
	return lipgloss.JoinVertical(lipgloss.Left,
		"", header, "",
		d.theme.Error.Render("  ✗ "+d.loadingErr), "",
		d.theme.Footer.Render("  q quit"),
	)
}

func (d *Dashboard) viewYouTube(header string) string {
	if d.phase == phaseSelect {
		return d.viewYouTubeSelect(header)
	}
	return d.viewYouTubeProgress(header)
}

func (d *Dashboard) viewYouTubeSelect(header string) string {
	titleLine := d.theme.Dim.Render("  video  ") + d.theme.Bright.Render(d.ytTitle)
	tablePanel := d.theme.PanelSlim.Render(d.table.View())
	footer := d.theme.Footer.Render("  ↑↓ select quality  •  enter download  •  q quit")
	return lipgloss.JoinVertical(lipgloss.Left,
		"", header, "",
		titleLine, "", tablePanel, "", footer,
	)
}

func (d *Dashboard) viewYouTubeProgress(header string) string {
	var pct float64
	if d.ytTotal > 0 {
		pct = float64(d.ytBytes) / float64(d.ytTotal)
		if pct > 1 {
			pct = 1
		}
	}

	statusIcon := d.spinner.View()
	statusText := "downloading"
	if d.phase == phaseDone {
		if d.ytFailed {
			statusIcon = "✗"
			statusText = "failed"
		} else {
			statusIcon = "✓"
			statusText = "complete"
		}
	}

	titleLine := d.theme.Dim.Render("  video  ") + d.theme.Bright.Render(d.ytTitle)

	progressBlock := lipgloss.JoinVertical(lipgloss.Left,
		fmt.Sprintf("  %s %s", statusIcon, d.theme.Accent.Render(statusText)),
		"",
		"  "+d.progress.ViewAs(pct),
		fmt.Sprintf("  %s / %s  ·  %.0f%%", humanizeBytes(d.ytBytes), humanizeBytes(d.ytTotal), pct*100),
		fmt.Sprintf("  %s/s  ·  ETA %s", humanizeBytes(int64(d.ytSpeed)), d.ytETA()),
	)

	footer := d.theme.Footer.Render("  q quit")

	sections := []string{"", header, "", titleLine, "", d.theme.PanelSlim.Render(progressBlock)}
	if d.status != "" {
		sections = append(sections, "", d.theme.Dim.Render("  "+d.status))
	}
	sections = append(sections, "", footer)
	return lipgloss.JoinVertical(lipgloss.Left, sections...)
}

func (d *Dashboard) viewBatch(header string) string {
	tablePanel := d.theme.PanelSlim.Render(d.table.View())
	stats := d.renderStats()
	footer := d.theme.Footer.Render("  q quit")

	sections := []string{"", header, "", stats, "", tablePanel}
	if d.status != "" {
		sections = append(sections, "", d.theme.Dim.Render("  "+d.status))
	}
	sections = append(sections, "", footer)
	return lipgloss.JoinVertical(lipgloss.Left, sections...)
}

func (d *Dashboard) renderStats() string {
	var downloading, completed, failed int
	var totalSpeed float64

	for _, st := range d.states {
		switch st.status {
		case model.StatusDownloading:
			downloading++
		case model.StatusCompleted:
			completed++
		case model.StatusFailed:
			failed++
		}
		if st.status == model.StatusDownloading {
			totalSpeed += st.speed
		}
	}

	total := len(d.states)
	parts := []string{
		fmt.Sprintf("%s/%s", d.theme.Value.Render(fmt.Sprintf("%d", completed)), d.theme.Dim.Render(fmt.Sprintf("%d", total))),
		d.theme.Dim.Render("active ") + d.theme.Cyan.Render(fmt.Sprintf("%d", downloading)),
		d.theme.Dim.Render("failed ") + d.theme.Error.Render(fmt.Sprintf("%d", failed)),
	}

	if totalSpeed > 0 {
		parts = append(parts, d.theme.Dim.Render("speed ")+d.theme.Cyan.Render(fmt.Sprintf("%s/s", humanizeBytes(int64(totalSpeed)))))
	}

	line := d.spinner.View() + " " + d.theme.Dim.Render("done ") + strings.Join(parts, "  ")
	return d.theme.StatusBar.Render(line)
}

func (d *Dashboard) ytETA() string {
	if d.ytTotal == 0 || d.ytBytes == 0 || d.ytSpeed <= 0 {
		return "—"
	}
	return formatDuration(time.Duration(float64(d.ytTotal-d.ytBytes)/d.ytSpeed) * time.Second)
}

// --- Helpers ---

func humanizeBytes(n int64) string {
	if n < 0 {
		return "?"
	}
	const u = 1024
	if n < u {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(u), 0
	for n2 := n / u; n2 >= u; n2 /= u {
		div *= u
		exp++
	}
	return fmt.Sprintf("%.1f%c", float64(n)/float64(div), "KMGTPE"[exp])
}

func formatDuration(d time.Duration) string {
	if d <= 0 {
		return "—"
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
}

func statusShort(s model.DownloadStatus) string {
	switch s {
	case model.StatusPending:
		return "queued"
	case model.StatusDownloading:
		return "active"
	case model.StatusCompleted:
		return "done"
	case model.StatusFailed:
		return "failed"
	default:
		return "—"
	}
}

func miniBar(st itemState) string {
	if st.status == model.StatusCompleted {
		return "▰▰▰▰▰▰▰▰▰▰ 100%"
	}
	if st.status == model.StatusPending {
		return "▱▱▱▱▱▱▱▱▱▱ 0%"
	}
	if st.status == model.StatusFailed {
		return "✗ failed"
	}
	if st.total <= 0 {
		return "▰▰▱▱▱▱▱▱▱▱ ???"
	}
	pct := float64(st.bytes) / float64(st.total)
	if pct > 1 {
		pct = 1
	}
	filled := int(pct * 10)
	return strings.Repeat("▰", filled) + strings.Repeat("▱", 10-filled) + fmt.Sprintf(" %3d%%", int(pct*100))
}

func truncate(s string, max int) string {
	// Slice by rune, not by byte: a byte-index slice can split a multi-byte
	// UTF-8 rune in half (scraped titles are frequently non-ASCII), which
	// produces a garbled/invalid string.
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}
