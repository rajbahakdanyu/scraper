package dashboard
// internal/ui/dashboard/dashboard.go
package dashboard

import (
	"fmt"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/user/scraper/internal/model"
	"github.com/user/scraper/internal/ui/styles"
)

// Downloader is the subset of download behavior the dashboard depends on.
// *downloader.Downloader satisfies this interface.
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
	selected bool
}

// Dashboard is a Bubble Tea model that displays a queue of media items and
// drives their downloads with live progress.
type Dashboard struct {
	table    table.Model
	progress progress.Model
	theme    styles.Theme

	states []itemState

	downloader Downloader
	dest       string

	width  int
	height int
	ready  bool

	quitting bool
	status   string
}

// New creates a Dashboard for the given items. dl may be nil if the
// dashboard is only used for display (no downloads will be triggered).
func New(items []model.MediaItem, dl Downloader, dest string) *Dashboard {
	columns := []table.Column{
		{Title: "", Width: 3},
		{Title: "Title", Width: 32},
		{Title: "Size", Width: 10},
		{Title: "Type", Width: 8},
		{Title: "Status", Width: 12},
	}

	states := make([]itemState, len(items))
	for i, it := range items {
		states[i] = itemState{item: it, status: model.StatusPending, total: it.Size}
	}

	height := len(items)
	if height > 15 {
		height = 15
	}
	if height < 3 {
		height = 3
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithFocused(true),
		table.WithHeight(height),
	)

	d := &Dashboard{
		table:      t,
		progress:   progress.New(progress.WithDefaultGradient()),
		theme:      styles.DefaultTheme(),
		states:     states,
		downloader: dl,
		dest:       dest,
	}
	d.refreshRows()
	return d
}

// Init starts listening for download progress events.
func (d *Dashboard) Init() tea.Cmd {
	cmds := []tea.Cmd{tea.EnterAltScreen}
	if d.downloader != nil {
		cmds = append(cmds, listenForProgress(d.downloader.Progress()))
	}
	return tea.Batch(cmds...)
}

// progressMsg carries a download progress update into the Bubble Tea loop.
type progressMsg model.ProgressEvent

// downloadDoneMsg reports the terminal outcome of a single item's download.
type downloadDoneMsg struct {
	url string
	err error
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

// Update handles key presses, window resizes, and progress events.
func (d *Dashboard) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		d.width = msg.Width
		d.height = msg.Height
		d.ready = true
		d.table.SetWidth(msg.Width)
		barWidth := msg.Width - 4
		if barWidth > 80 {
			barWidth = 80
		}
		if barWidth > 0 {
			d.progress.Width = barWidth
		}
		return d, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
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
	}

	var cmd tea.Cmd
	d.table, cmd = d.table.Update(msg)
	return d, cmd
}

func (d *Dashboard) applyProgress(evt model.ProgressEvent) {
	for i := range d.states {
		if d.states[i].item.URL == evt.Item.URL {
			d.states[i].status = evt.Status
			d.states[i].bytes = evt.Bytes
			if evt.Total > 0 {
				d.states[i].total = evt.Total
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
		d.status = fmt.Sprintf("failed: %s (%v)", msg.url, msg.err)
	} else {
		d.status = fmt.Sprintf("finished: %s", msg.url)
	}
}

// startSelectedDownloads kicks off downloads for the selected items, or all
// items if none are explicitly selected.
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
		d.states[i].status = model.StatusDownloading
		cmds = append(cmds, startDownload(d.downloader, d.states[i].item, d.dest))
	}
	d.refreshRows()

	return tea.Batch(cmds...)
}

func (d *Dashboard) refreshRows() {
	rows := make([]table.Row, len(d.states))
	for i, st := range d.states {
		mark := " "
		if st.selected {
			mark = "x"
		}
		rows[i] = table.Row{
			mark,
			st.item.Title,
			humanizeBytes(st.total),
			string(st.item.Type),
			statusLabel(st.status),
		}
	}
	d.table.SetRows(rows)
}

// View renders the dashboard.
func (d *Dashboard) View() string {
	if d.quitting {
		return ""
	}

	header := d.theme.Header.Render(fmt.Sprintf("Scraper — %d item(s)", len(d.states)))
	help := d.theme.Secondary.Render("↑/↓ navigate • space select • enter download • q quit")
	bar := d.progress.ViewAs(d.overallPercent())

	sections := []string{header, d.table.View(), bar}
	if d.status != "" {
		sections = append(sections, d.status)
	}
	sections = append(sections, help)

	return lipgloss.JoinVertical(lipgloss.Left, sections...)
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

func statusLabel(s model.DownloadStatus) string {
	switch s {
	case model.StatusPending:
		return "pending"
	case model.StatusDownloading:
		return "downloading"
	case model.StatusCompleted:
		return "done"
	case model.StatusFailed:
		return "failed"
	case model.StatusCancelled:
		return "cancelled"
	default:
		return "unknown"
	}
}

func humanizeBytes(n int64) string {
	const unit = 1024
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
