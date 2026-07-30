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
	Cancel(item model.MediaItem)
	Progress() <-chan model.ProgressEvent
}

type phase int

const (
	phaseLoading phase = iota
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

	sendFunc  func(tea.Msg)
	albumSlug string
}

// Futuristic logo — clean, minimal
const logo = "◢◤  SCRAPER  ◢◤"

func New(scraperFn func() ([]model.MediaItem, error), dl Downloader, dest string, site string) *Dashboard {
	columns := []table.Column{
		{Title: "", Width: 2},
		{Title: "FILE", Width: 42},
		{Title: "STATUS", Width: 12},
		{Title: "PROGRESS", Width: 16},
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
		if !d.spinnerActive() {
			return d, nil
		}
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
		case "c":
			d.cancelSelected()
			return d, nil
		}

	case progressMsg:
		d.applyProgress(model.ProgressEvent(msg))
		d.refreshRows()
		return d, nil

	case downloadDoneMsg:
		d.applyDone(msg)
		d.refreshRows()
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

	d.phase = phaseDownload
	d.refreshRows()

	var cmds []tea.Cmd
	if d.downloader != nil {
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

// --- Batch ---

// cancelSelected cancels the currently-highlighted item's download, if
// it's still in progress.
func (d *Dashboard) cancelSelected() {
	if d.downloader == nil {
		return
	}
	idx := d.table.Cursor()
	if idx < 0 || idx >= len(d.states) {
		return
	}
	if d.states[idx].status != model.StatusDownloading {
		return
	}
	d.downloader.Cancel(d.states[idx].item)
}

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
		switch {
		case msg.err == nil:
			d.states[i].status = model.StatusCompleted
			if d.states[i].total > 0 {
				d.states[i].bytes = d.states[i].total
			}
		case d.states[i].status == model.StatusCancelled:
			// Already marked cancelled via the progress channel; a
			// cancelled download also returns an error here, but that's
			// not a failure worth downgrading the status for.
		default:
			d.states[i].status = model.StatusFailed
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
		case model.StatusCancelled:
			mark = "⊘"
		case model.StatusPending:
			mark = "·"
		}

		rows[i] = table.Row{mark, truncate(st.item.Title, 40), statusShort(st.status), miniBar(st)}
	}
	d.table.SetRows(rows)
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

func (d *Dashboard) viewBatch(header string) string {
	tablePanel := d.theme.PanelSlim.Render(d.table.View())
	stats := d.renderStats()
	footer := d.theme.Footer.Render("  ↑↓ select  •  c cancel  •  q quit")

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
		parts = append(parts, d.theme.Dim.Render("speed ")+d.theme.Cyan.Render(fmt.Sprintf("%s/s", HumanizeBytes(int64(totalSpeed)))))
	}

	icon := d.spinner.View()
	if !d.spinnerActive() {
		icon = d.theme.Success.Render("✓")
		if failed > 0 {
			icon = d.theme.Error.Render("✗")
		}
	}

	line := icon + " " + d.theme.Dim.Render("done ") + strings.Join(parts, "  ")
	return d.theme.StatusBar.Render(line)
}

// spinnerActive reports whether the spinner should keep ticking: while
// media is loading, or while any batch item is still downloading. Once
// everything's done, ticking stops instead of animating forever.
func (d *Dashboard) spinnerActive() bool {
	if d.phase == phaseLoading {
		return true
	}
	for _, st := range d.states {
		if st.status == model.StatusDownloading {
			return true
		}
	}
	return false
}

// --- Helpers ---

func HumanizeBytes(n int64) string {
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
	case model.StatusCancelled:
		return "cancelled"
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
	if st.status == model.StatusCancelled {
		return "⊘ cancelled"
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
