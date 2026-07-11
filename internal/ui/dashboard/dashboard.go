// internal/ui/dashboard/dashboard.go
package dashboard

import (
	"fmt"
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

// Downloader is the subset of download behavior the dashboard depends on.
type Downloader interface {
	Download(item model.MediaItem, dest string) error
	Progress() <-chan model.ProgressEvent
}

// itemState tracks the live download state of a single queued item.
type itemState struct {
	item     model.MediaItem
	status   model.DownloadStatus
	bytes    int64
	total    int64
	speed    float64
	selected bool
	started  time.Time
}

// YouTubeDownloader is an optional interface for scrapers that handle
// their own download with quality selection (e.g. video+audio muxing).
type YouTubeDownloader interface {
	DownloadVideoWithQuality(videoID, dest string, height int) error
}

// Dashboard is a Bubble Tea model that displays a queue of media items and
// drives their downloads with live progress, stats, and rich styling.
type Dashboard struct {
	table    table.Model
	progress progress.Model
	spinner  spinner.Model
	theme    styles.Theme

	states []itemState

	downloader Downloader
	dest       string

	width  int
	height int
	ready  bool

	quitting  bool
	status    string
	site      string
	autoStart bool

	ytDownloader  YouTubeDownloader
	ytVideoID     string
	ytDownloading bool
}

const banner = `
 ___  ___  ___  ___  __   ___ ___ ___
| _ \/ _ \| _ \/ __|  \ / __| __/ __|
|  _/ (_) |   / (__    | (__| _|\__ \
|_|  \___/|_|_\\___|  / \___|___|___/
                  |__/`

// New creates a Dashboard for the given items.
func New(items []model.MediaItem, dl Downloader, dest string, site string) *Dashboard {
	columns := []table.Column{
		{Title: "  ", Width: 3},
		{Title: "Title", Width: 36},
		{Title: "Size", Width: 10},
		{Title: "Type", Width: 7},
		{Title: "Status", Width: 13},
		{Title: "Progress", Width: 12},
	}

	states := make([]itemState, len(items))
	for i, it := range items {
		states[i] = itemState{item: it, status: model.StatusPending, total: it.Size}
	}

	height := len(items)
	if height > 15 {
		height = 15
	}
	if height < 4 {
		height = 4
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithFocused(true),
		table.WithHeight(height),
	)

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))

	d := &Dashboard{
		table:      t,
		progress:   progress.New(progress.WithDefaultGradient()),
		spinner:    sp,
		theme:      styles.DefaultTheme(),
		states:     states,
		downloader: dl,
		dest:       dest,
		site:       site,
		autoStart:  site != "youtube",
	}
	d.refreshRows()
	return d
}

// SetYouTubeDownloader enables quality-select mode for YouTube downloads.
func (d *Dashboard) SetYouTubeDownloader(yd YouTubeDownloader, videoID string) {
	d.ytDownloader = yd
	d.ytVideoID = videoID
}

// AutoStart marks all items for download immediately on Init.
func (d *Dashboard) AutoStart() {
	d.autoStart = true
}

// Init starts the spinner and listening for download progress events.
func (d *Dashboard) Init() tea.Cmd {
	cmds := []tea.Cmd{
		tea.EnterAltScreen,
		d.spinner.Tick,
	}
	if d.downloader != nil {
		cmds = append(cmds, listenForProgress(d.downloader.Progress()))
	}
	if d.autoStart {
		cmds = append(cmds, d.startSelectedDownloads())
	}
	return tea.Batch(cmds...)
}

type progressMsg model.ProgressEvent

type downloadDoneMsg struct {
	url string
	err error
}

type tickMsg time.Time

func listenForProgress(ch <-chan model.ProgressEvent) tea.Cmd {
	return func() tea.Msg {
		evt, ok := <-ch
		if !ok {
			return nil
		}
		return progressMsg(evt)
	}
}

func startDownload(dl Downloader, item model.MediaItem, dest string) tea.Cmd {
	return func() tea.Msg {
		err := dl.Download(item, dest)
		return downloadDoneMsg{url: item.URL, err: err}
	}
}

// youtubeDoneMsg signals a YouTube quality-selected download has finished.
type youtubeDoneMsg struct {
	err error
}

func (d *Dashboard) startYouTubeDownload() tea.Cmd {
	idx := d.table.Cursor()
	if idx < 0 || idx >= len(d.states) {
		return nil
	}

	// Extract height from the item's Thumbnail field (which stores itag as string).
	// We parse the quality label from the title.
	height := d.parseHeightFromTitle(d.states[idx].item.Title)
	d.ytDownloading = true

	// Mark all as pending except the selected one.
	for i := range d.states {
		if i == idx {
			d.states[i].status = model.StatusDownloading
			d.states[i].started = time.Now()
		} else {
			d.states[i].status = model.StatusCancelled
		}
	}
	d.refreshRows()

	return func() tea.Msg {
		err := d.ytDownloader.DownloadVideoWithQuality(d.ytVideoID, d.dest, height)
		return youtubeDoneMsg{err: err}
	}
}

func (d *Dashboard) parseHeightFromTitle(title string) int {
	// Title format: "Video Title [1080p] (video-only (will mux audio))"
	idx := strings.Index(title, "[")
	if idx < 0 {
		return 0
	}
	end := strings.Index(title[idx:], "p]")
	if end < 0 {
		return 0
	}
	heightStr := title[idx+1 : idx+end]
	var h int
	fmt.Sscanf(heightStr, "%d", &h)
	return h
}

func (d *Dashboard) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		d.width = msg.Width
		d.height = msg.Height
		d.ready = true
		d.table.SetWidth(msg.Width - 4)
		barWidth := msg.Width - 4
		if barWidth > 80 {
			barWidth = 80
		}
		if barWidth > 0 {
			d.progress.Width = barWidth
		}
		return d, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		d.spinner, cmd = d.spinner.Update(msg)
		return d, cmd

	case tickMsg:
		return d, tea.Tick(time.Second, func(t time.Time) tea.Msg {
			return tickMsg(t)
		})

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			d.quitting = true
			return d, tea.Quit
		case " ":
			idx := d.table.Cursor()
			if idx >= 0 && idx < len(d.states) {
				d.states[idx].selected = !d.states[idx].selected
				d.refreshRows()
			}
			return d, nil
		case "enter":
			if d.ytDownloader != nil {
				if d.ytDownloading {
					return d, nil
				}
				return d, d.startYouTubeDownload()
			}
			return d, d.startSelectedDownloads()
		}

	case progressMsg:
		d.applyProgress(model.ProgressEvent(msg))
		d.refreshRows()
		if d.downloader != nil {
			return d, listenForProgress(d.downloader.Progress())
		}
		return d, nil

	case downloadDoneMsg:
		d.applyDone(msg)
		d.refreshRows()
		return d, nil

	case youtubeDoneMsg:
		d.ytDownloading = false
		if msg.err != nil {
			for i := range d.states {
				if d.states[i].status == model.StatusDownloading {
					d.states[i].status = model.StatusFailed
				}
			}
			d.status = fmt.Sprintf("  ✗ failed: %v", msg.err)
		} else {
			for i := range d.states {
				if d.states[i].status == model.StatusDownloading {
					d.states[i].status = model.StatusCompleted
					d.states[i].bytes = d.states[i].total
				}
			}
			d.status = "  ✓ download complete"
		}
		d.refreshRows()
		return d, nil
	}

	var cmd tea.Cmd
	d.table, cmd = d.table.Update(msg)
	return d, cmd
}

func (d *Dashboard) applyProgress(evt model.ProgressEvent) {
	for i := range d.states {
		if d.states[i].item.URL == evt.Item.URL {
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
}

func (d *Dashboard) applyDone(msg downloadDoneMsg) {
	for i := range d.states {
		if d.states[i].item.URL != msg.url {
			continue
		}
		if msg.err != nil {
			d.states[i].status = model.StatusFailed
		} else if d.states[i].status != model.StatusCompleted {
			d.states[i].status = model.StatusCompleted
		}
		break
	}

	if msg.err != nil {
		d.status = fmt.Sprintf("  ✗ failed: %s (%v)", msg.url, msg.err)
	} else {
		d.status = fmt.Sprintf("  ✓ finished: %s", msg.url)
	}
}

func (d *Dashboard) startSelectedDownloads() tea.Cmd {
	if d.downloader == nil {
		return nil
	}

	anySelected := false
	for _, st := range d.states {
		if st.selected {
			anySelected = true
			break
		}
	}

	var cmds []tea.Cmd
	for i := range d.states {
		if anySelected && !d.states[i].selected {
			continue
		}
		if d.states[i].status == model.StatusDownloading || d.states[i].status == model.StatusCompleted {
			continue
		}
		d.states[i].status = model.StatusDownloading
		d.states[i].started = time.Now()
		cmds = append(cmds, startDownload(d.downloader, d.states[i].item, d.dest))
	}
	d.refreshRows()

	return tea.Batch(cmds...)
}

func (d *Dashboard) refreshRows() {
	rows := make([]table.Row, len(d.states))
	for i, st := range d.states {
		mark := "○"
		if st.selected {
			mark = "●"
		}
		rows[i] = table.Row{
			mark,
			truncate(st.item.Title, 34),
			humanizeBytes(st.total),
			mediaTypeIcon(st.item.Type),
			statusLabel(st.status),
			progressBar(st),
		}
	}
	d.table.SetRows(rows)
}

// View renders the full dashboard.
func (d *Dashboard) View() string {
	if d.quitting {
		return ""
	}
	if !d.ready {
		return d.spinner.View() + "  Loading..."
	}

	bannerStyled := lipgloss.NewStyle().
		Foreground(lipgloss.Color("213")).
		Render(banner)

	siteLabel := d.theme.Subtitle.Render(
		fmt.Sprintf("  Site: %s  •  Items: %d  •  Output: %s", d.site, len(d.states), d.dest),
	)

	header := lipgloss.JoinVertical(lipgloss.Left, bannerStyled, siteLabel)

	tablePanel := d.theme.Border.Render(d.table.View())

	stats := d.renderStats()

	bar := d.progress.ViewAs(d.overallPercent())
	progressPanel := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("99")).
		Padding(0, 1).
		Render(
			lipgloss.JoinVertical(lipgloss.Left,
				lipgloss.NewStyle().Foreground(lipgloss.Color("243")).Render("Overall Progress"),
				bar,
			),
		)

	var footer string
	if d.ytDownloader != nil && !d.ytDownloading {
		footer = d.theme.Footer.Render(
			"  ↑/↓ select quality  •  enter download  •  q quit  ",
		)
	} else if d.ytDownloading {
		footer = d.theme.Footer.Render(
			"  downloading...  •  q quit  ",
		)
	} else {
		footer = d.theme.Footer.Render(
			"  ↑/↓ navigate  •  q quit  ",
		)
	}

	sections := []string{
		header,
		"",
		stats,
		"",
		tablePanel,
		"",
		progressPanel,
	}

	if d.status != "" {
		sections = append(sections, "", d.status)
	}

	sections = append(sections, "", footer)

	return lipgloss.JoinVertical(lipgloss.Left, sections...)
}

// renderStats produces a compact stats panel.
func (d *Dashboard) renderStats() string {
	var pending, downloading, completed, failed int
	for _, st := range d.states {
		switch st.status {
		case model.StatusPending:
			pending++
		case model.StatusDownloading:
			downloading++
		case model.StatusCompleted:
			completed++
		case model.StatusFailed:
			failed++
		}
	}

	total := len(d.states)
	var downloadedBytes, totalBytes int64
	for _, st := range d.states {
		downloadedBytes += st.bytes
		totalBytes += st.total
	}

	spin := d.spinner.View()

	stat := func(label, value string) string {
		return fmt.Sprintf("  %s%s  %s",
			d.theme.StatLabel.Render(label),
			d.theme.Muted.Render(":"),
			d.theme.StatValue.Render(value),
		)
	}

	var totalSpeed float64
	for _, st := range d.states {
		if st.status == model.StatusDownloading {
			totalSpeed += st.speed
		}
	}

	statsLine1 := fmt.Sprintf("%s %s  %s  %s  %s  %s  %s  %s",
		spin,
		stat("Pending", fmt.Sprintf("%d/%d", pending, total)),
		stat("Active", fmt.Sprintf("%d", downloading)),
		stat("Done", fmt.Sprintf("%d", completed)),
		stat("Failed", fmt.Sprintf("%d", failed)),
		stat("Speed", fmt.Sprintf("%s/s", humanizeBytes(int64(totalSpeed)))),
		stat("Size", fmt.Sprintf("%s / %s", humanizeBytes(downloadedBytes), humanizeBytes(totalBytes))),
		stat("ETA", d.eta()),
	)

	return d.theme.StatusBar.Render(statsLine1)
}

func (d *Dashboard) overallPercent() float64 {
	var total, done int64
	for _, st := range d.states {
		if st.total > 0 {
			total += st.total
			done += st.bytes
		}
	}
	if total == 0 {
		return 0
	}
	return float64(done) / float64(total)
}

func (d *Dashboard) eta() string {
	var downloadedBytes, totalBytes int64
	var elapsed time.Duration
	active := 0
	for _, st := range d.states {
		if st.total > 0 {
			totalBytes += st.total
			downloadedBytes += st.bytes
		}
		if st.status == model.StatusDownloading && !st.started.IsZero() {
			elapsed += time.Since(st.started)
			active++
		}
	}
	if totalBytes == 0 || downloadedBytes == 0 || active == 0 {
		return "—"
	}
	speed := float64(downloadedBytes) / elapsed.Seconds()
	if speed <= 0 {
		return "—"
	}
	remaining := float64(totalBytes-downloadedBytes) / speed
	return formatDuration(time.Duration(remaining) * time.Second)
}

func statusLabel(s model.DownloadStatus) string {
	switch s {
	case model.StatusPending:
		return "○ pending"
	case model.StatusDownloading:
		return "◐ downloading"
	case model.StatusCompleted:
		return "✓ done"
	case model.StatusFailed:
		return "✗ failed"
	case model.StatusCancelled:
		return "⊘ cancelled"
	default:
		return "? unknown"
	}
}

func mediaTypeIcon(t model.MediaType) string {
	switch t {
	case model.MediaTypeVideo:
		return "🎬 video"
	case model.MediaTypeImage:
		return "🖼 image"
	default:
		return "📁 file"
	}
}

func progressBar(st itemState) string {
	if st.status == model.StatusPending {
		return "░░░░░░░░░░   0%"
	}
	if st.status == model.StatusCompleted {
		return "██████████ 100%"
	}
	if st.total <= 0 {
		return "▒▒▒▒▒▒▒▒▒▒  ??%"
	}
	pct := float64(st.bytes) / float64(st.total)
	if pct > 1 {
		pct = 1
	}
	return fmt.Sprintf("%s %3d%%", dotedBar(pct), int(pct*100))
}

const barWidth = 10

func dotedBar(pct float64) string {
	filled := int(pct * float64(barWidth))
	if filled > barWidth {
		filled = barWidth
	}
	bar := strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)
	return bar
}

func humanizeBytes(n int64) string {
	const unit = 1024
	if n < 0 {
		return "?"
	}
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for n2 := n / unit; n2 >= unit; n2 /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%ciB", float64(n)/float64(div), "KMGTPE"[exp])
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

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}
