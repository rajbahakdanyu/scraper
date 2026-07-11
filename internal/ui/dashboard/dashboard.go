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

// YouTubeDownloader is an optional interface for scrapers that handle
// their own download with quality selection (e.g. video+audio muxing).
type YouTubeDownloader interface {
	DownloadVideoWithQuality(videoID, dest string, height int) error
}

// phase indicates which screen the dashboard is showing.
type phase int

const (
	phaseLoading  phase = iota // fetching media items
	phaseSelect                // quality / item selection
	phaseDownload              // downloading with progress
	phaseDone                  // finished
)

// itemState tracks the live download state of a single queued item.
type itemState struct {
	item    model.MediaItem
	status  model.DownloadStatus
	bytes   int64
	total   int64
	speed   float64
	started time.Time
}

// Dashboard is a Bubble Tea model with two phases:
//
//	YouTube: select quality → download single video with progress
//	Other:   auto-download all items with live progress table
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

	quitting bool
	status   string
	site     string
	phase    phase

	// Loading phase
	scraperFn  func() ([]model.MediaItem, error)
	scrapeURL  string
	loadingErr string

	// YouTube-specific
	ytDownloader YouTubeDownloader
	ytVideoID    string
	ytTitle      string
	ytBytes      int64
	ytTotal      int64
	ytSpeed      float64
	ytStarted    time.Time
	ytFailed     bool
	sendFunc     func(tea.Msg)
}

const banner = `
 ___  ___  ___  ___  __   ___ ___ ___
| _ \/ _ \| _ \/ __|  \ / __| __/ __|
|  _/ (_) |   / (__    | (__| _|\__ \
|_|  \___/|_|_\\___|  / \___|___|___/
                  |__/`

// New creates a Dashboard. The scraperFn is called in the background to
// fetch media items; the TUI shows a loading spinner until results arrive.
func New(scraperFn func() ([]model.MediaItem, error), dl Downloader, dest string, site string) *Dashboard {
	isYouTube := site == "youtube"

	var columns []table.Column
	if isYouTube {
		columns = []table.Column{
			{Title: "  ", Width: 3},
			{Title: "Quality", Width: 12},
			{Title: "Size", Width: 10},
			{Title: "Note", Width: 28},
		}
	} else {
		columns = []table.Column{
			{Title: "  ", Width: 3},
			{Title: "Filename", Width: 40},
			{Title: "Type", Width: 8},
			{Title: "Status", Width: 14},
			{Title: "Progress", Width: 14},
		}
	}

	height := 4

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
		downloader: dl,
		dest:       dest,
		site:       site,
		phase:      phaseLoading,
		scraperFn:  scraperFn,
	}
	return d
}

// SetYouTubeDownloader enables quality-select mode for YouTube downloads.
func (d *Dashboard) SetYouTubeDownloader(yd YouTubeDownloader, videoID string) {
	d.ytDownloader = yd
	d.ytVideoID = videoID
}

// SetSendFunc allows the dashboard to send messages to the tea.Program.
func (d *Dashboard) SetSendFunc(send func(tea.Msg)) {
	d.sendFunc = send
}

// Init starts the spinner and kicks off the background scrape.
func (d *Dashboard) Init() tea.Cmd {
	return tea.Batch(
		tea.EnterAltScreen,
		d.spinner.Tick,
		d.startScrape(),
	)
}

// handleScrapeResult processes the background scrape result, populates the
// table, and transitions to the appropriate phase.
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

	// Populate states
	d.states = make([]itemState, len(msg.items))
	for i, it := range msg.items {
		d.states[i] = itemState{item: it, status: model.StatusPending, total: it.Size}
	}

	height := len(msg.items)
	if height > 15 {
		height = 15
	}
	if height < 4 {
		height = 4
	}
	d.table.SetHeight(height)

	isYouTube := d.site == "youtube"
	if isYouTube {
		d.phase = phaseSelect
	} else {
		d.phase = phaseDownload
	}
	d.refreshRows()

	// Auto-start downloads for non-YouTube
	var cmds []tea.Cmd
	if !isYouTube && d.downloader != nil {
		cmds = append(cmds, listenForProgress(d.downloader.Progress()))
		cmds = append(cmds, d.startAllDownloads())
	}
	return d, tea.Batch(cmds...)
}

// startScrape runs the scraper in a background goroutine and sends the
// results back as a scrapeResultMsg.
func (d *Dashboard) startScrape() tea.Cmd {
	return func() tea.Msg {
		items, err := d.scraperFn()
		return scrapeResultMsg{items: items, err: err}
	}
}

type progressMsg model.ProgressEvent

type downloadDoneMsg struct {
	url string
	err error
}

type youtubeDoneMsg struct {
	err error
}

type tickMsg time.Time

// scrapeResultMsg carries the result of the background scrape operation.
type scrapeResultMsg struct {
	items []model.MediaItem
	err   error
}

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

func (d *Dashboard) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		d.width = msg.Width
		d.height = msg.Height
		d.ready = true
		d.table.SetWidth(msg.Width - 4)
		barWidth := msg.Width - 8
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
			if d.downloader != nil {
				return d, listenForProgress(d.downloader.Progress())
			}
		}
		return d, nil

	case downloadDoneMsg:
		d.applyDone(msg)
		d.refreshRows()
		return d, nil

	case youtubeDoneMsg:
		if msg.err != nil {
			d.ytFailed = true
			d.status = fmt.Sprintf("✗ failed: %v", msg.err)
			d.phase = phaseDone
		} else {
			d.ytBytes = d.ytTotal
			d.status = "✓ download complete"
			d.phase = phaseDone
		}
		return d, nil
	}

	var cmd tea.Cmd
	d.table, cmd = d.table.Update(msg)
	return d, cmd
}

// --- YouTube download ---

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
			yt.SetProgressFunc(func(bytes int64, total int64) {
				if d.sendFunc != nil {
					d.sendFunc(progressMsg(model.ProgressEvent{
						Bytes:  bytes,
						Total:  total,
						Status: model.StatusDownloading,
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
	heightStr := title[idx+1 : idx+end]
	var h int
	fmt.Sscanf(heightStr, "%d", &h)
	return h
}

func (d *Dashboard) extractTitle(title string) string {
	idx := strings.Index(title, " [")
	if idx < 0 {
		return title
	}
	return title[:idx]
}

// --- Batch download (non-YouTube) ---

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
		} else {
			d.states[i].status = model.StatusCompleted
			d.states[i].bytes = d.states[i].total
		}
		break
	}
	if msg.err != nil {
		d.status = fmt.Sprintf("✗ failed: %s", msg.url)
	} else {
		d.status = fmt.Sprintf("✓ finished: %s", msg.url)
	}
}

// --- Rendering ---

func (d *Dashboard) refreshRows() {
	rows := make([]table.Row, len(d.states))
	for i, st := range d.states {
		mark := "○"
		if st.status == model.StatusDownloading {
			mark = "◐"
		} else if st.status == model.StatusCompleted {
			mark = "✓"
		} else if st.status == model.StatusFailed {
			mark = "✗"
		}

		if d.site == "youtube" {
			quality, note := d.splitQualityNote(st.item.Title)
			rows[i] = table.Row{mark, quality, humanizeBytes(st.total), note}
		} else {
			rows[i] = table.Row{
				mark,
				truncate(st.item.Title, 38),
				mediaTypeIcon(st.item.Type),
				statusLabel(st.status),
				batchProgressBar(st),
			}
		}
	}
	d.table.SetRows(rows)
}

func (d *Dashboard) splitQualityNote(title string) (quality, note string) {
	// Title format: "Video Title [1080p] (video-only (will mux audio))"
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

func (d *Dashboard) View() string {
	if d.quitting {
		return ""
	}

	bannerStyled := lipgloss.NewStyle().
		Foreground(lipgloss.Color("213")).
		Render(banner)

	siteLabel := d.theme.Subtitle.Render(
		fmt.Sprintf("  Site: %s  •  Output: %s", d.site, d.dest),
	)

	header := lipgloss.JoinVertical(lipgloss.Left, bannerStyled, siteLabel)

	if d.phase == phaseLoading {
		return d.viewLoading(header)
	}

	if d.phase == phaseDone && d.loadingErr != "" && len(d.states) == 0 {
		return lipgloss.JoinVertical(lipgloss.Left,
			header, "",
			d.theme.Error.Render("  ✗ "+d.loadingErr), "",
			d.theme.Footer.Render("  q quit  "),
		)
	}

	if d.ytDownloader != nil {
		return d.viewYouTube(header)
	}
	return d.viewBatch(header)
}

func (d *Dashboard) viewLoading(header string) string {
	msg := d.spinner.View() + "  Fetching media items..."
	return lipgloss.JoinVertical(lipgloss.Left,
		header, "",
		lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("99")).
			Padding(1, 2).
			Render(msg),
		"",
		d.theme.Footer.Render("  q quit  "),
	)
}

// viewYouTube renders the two-phase YouTube flow.
func (d *Dashboard) viewYouTube(header string) string {
	if d.phase == phaseSelect {
		return d.viewYouTubeSelect(header)
	}
	return d.viewYouTubeProgress(header)
}

func (d *Dashboard) viewYouTubeSelect(header string) string {
	titleBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("99")).
		Padding(0, 2).
		Render(d.theme.Highlight.Render("Video: ") + d.ytTitle)

	tablePanel := d.theme.Border.Render(d.table.View())

	instruction := d.theme.Footer.Render(
		"  ↑/↓ select quality  •  enter download  •  q quit  ",
	)

	return lipgloss.JoinVertical(lipgloss.Left,
		header, "",
		titleBox, "",
		lipgloss.NewStyle().Foreground(lipgloss.Color("243")).Render("  Available qualities:"),
		tablePanel, "",
		instruction,
	)
}

func (d *Dashboard) viewYouTubeProgress(header string) string {
	titleBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("99")).
		Padding(0, 2).
		Render(d.theme.Highlight.Render("Video: ") + d.ytTitle)

	var pct float64
	if d.ytTotal > 0 {
		pct = float64(d.ytBytes) / float64(d.ytTotal)
		if pct > 1 {
			pct = 1
		}
	}

	bigBar := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("63")).
		Padding(1, 2).
		Render(
			lipgloss.JoinVertical(lipgloss.Left,
				d.spinner.View()+" "+statusLabelYT(d.phase, d.ytFailed),
				"",
				d.progress.ViewAs(pct),
				fmt.Sprintf("  %s / %s  (%.0f%%)", humanizeBytes(d.ytBytes), humanizeBytes(d.ytTotal), pct*100),
				fmt.Sprintf("  Speed: %s/s  •  ETA: %s", humanizeBytes(int64(d.ytSpeed)), d.ytETA()),
			),
		)

	footer := d.theme.Footer.Render("  q quit  ")

	sections := []string{header, "", titleBox, "", bigBar}
	if d.status != "" {
		sections = append(sections, "", d.status)
	}
	sections = append(sections, "", footer)

	return lipgloss.JoinVertical(lipgloss.Left, sections...)
}

// viewBatch renders the multi-file batch download view.
func (d *Dashboard) viewBatch(header string) string {
	tablePanel := d.theme.Border.Render(d.table.View())

	stats := d.renderStats()

	bigBar := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("63")).
		Padding(0, 1).
		Render(
			lipgloss.JoinVertical(lipgloss.Left,
				lipgloss.NewStyle().Foreground(lipgloss.Color("243")).Render("Overall Progress"),
				d.progress.ViewAs(d.overallPercent()),
			),
		)

	footer := d.theme.Footer.Render("  q quit  ")

	sections := []string{header, "", stats, "", tablePanel, "", bigBar}
	if d.status != "" {
		sections = append(sections, "", d.status)
	}
	sections = append(sections, "", footer)

	return lipgloss.JoinVertical(lipgloss.Left, sections...)
}

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
	var totalSpeed float64
	for _, st := range d.states {
		downloadedBytes += st.bytes
		totalBytes += st.total
		if st.status == model.StatusDownloading {
			totalSpeed += st.speed
		}
	}

	spin := d.spinner.View()
	stat := func(label, value string) string {
		return fmt.Sprintf("  %s%s %s",
			d.theme.StatLabel.Render(label),
			d.theme.Muted.Render(":"),
			d.theme.StatValue.Render(value),
		)
	}

	line := fmt.Sprintf("%s %s %s %s %s %s %s %s",
		spin,
		stat("Pending", fmt.Sprintf("%d/%d", pending, total)),
		stat("Active", fmt.Sprintf("%d", downloading)),
		stat("Done", fmt.Sprintf("%d", completed)),
		stat("Failed", fmt.Sprintf("%d", failed)),
		stat("Speed", fmt.Sprintf("%s/s", humanizeBytes(int64(totalSpeed)))),
		stat("Size", fmt.Sprintf("%s/%s", humanizeBytes(downloadedBytes), humanizeBytes(totalBytes))),
		stat("ETA", d.eta()),
	)

	return d.theme.StatusBar.Render(line)
}

func (d *Dashboard) overallPercent() float64 {
	var total, done int64
	var itemsWithSize, completedItems, totalItems int
	for _, st := range d.states {
		totalItems++
		if st.total > 0 {
			total += st.total
			done += st.bytes
			itemsWithSize++
		}
		if st.status == model.StatusCompleted {
			completedItems++
		}
	}

	// If we have size info, use byte-based progress
	if total > 0 {
		return float64(done) / float64(total)
	}

	// No size info — use item-count-based progress
	if totalItems == 0 {
		return 0
	}
	return float64(completedItems) / float64(totalItems)
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

func (d *Dashboard) ytETA() string {
	if d.ytTotal == 0 || d.ytBytes == 0 || d.ytSpeed <= 0 {
		return "—"
	}
	remaining := float64(d.ytTotal-d.ytBytes) / d.ytSpeed
	return formatDuration(time.Duration(remaining) * time.Second)
}

func statusLabelYT(p phase, failed bool) string {
	switch p {
	case phaseDownload:
		return "Downloading..."
	case phaseDone:
		if failed {
			return "✗ Failed"
		}
		return "✓ Complete"
	default:
		return "Ready"
	}
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
		return "🎬"
	case model.MediaTypeImage:
		return "🖼"
	default:
		return "📁"
	}
}

func batchProgressBar(st itemState) string {
	if st.status == model.StatusCompleted {
		return "██████████ 100%"
	}
	if st.status == model.StatusPending {
		return "░░░░░░░░░░   0%"
	}
	if st.status == model.StatusFailed {
		return "✗ failed"
	}
	if st.total <= 0 {
		return "▒▒▒▒▒▒▒▒▒▒ ???"
	}
	pct := float64(st.bytes) / float64(st.total)
	if pct > 1 {
		pct = 1
	}
	return fmt.Sprintf("%s %3d%%", barString(pct, 10), int(pct*100))
}

func barString(pct float64, width int) string {
	filled := int(pct * float64(width))
	if filled > width {
		filled = width
	}
	return strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}
