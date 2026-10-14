// internal/ui/dashboard/dashboard.go
package dashboard

import (
	"context"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/rajbahakdanyu/scraper/internal/model"
	"github.com/rajbahakdanyu/scraper/internal/session"
	"github.com/rajbahakdanyu/scraper/internal/ui/styles"
)

// Dashboard is the Bubble Tea frontend for a session: run state lives in
// the embedded Session, the Dashboard only adds terminal concerns.
type Dashboard struct {
	*session.Session

	table    table.Model
	progress progress.Model
	spinner  spinner.Model
	theme    styles.Theme

	rows         []table.Row
	visible      []int
	visibleReady bool

	width  int
	height int
	ready  bool

	quitting          bool
	showErrors        bool
	searching         bool
	searchQuery       string
	inspectMsg        string
	refreshTickActive bool
	writeRetryList    func([]model.FailedItem) (string, error)
}

// Futuristic logo — clean, minimal
const logo = "◢◤  SCRAPER  ◢◤"

func New(scraperFn func(context.Context) ([]model.MediaItem, error), dl model.Downloader, dest string, site string) *Dashboard {
	return newDashboard(session.New(scraperFn, dl, dest, site))
}

// NewBatch creates a dashboard for batch mode. Items are appended
// incrementally as each album is scraped.
func NewBatch(dl model.Downloader, destRoot string, albums []model.Album) *Dashboard {
	return newDashboard(session.NewBatch(dl, destRoot, albums))
}

func newDashboard(s *session.Session) *Dashboard {
	t := table.New(
		table.WithColumns([]table.Column{
			{Title: "", Width: 2},
			{Title: "FILE", Width: 34},
			{Title: "STATUS", Width: 12},
			{Title: "PROGRESS", Width: 26},
		}),
		table.WithFocused(true),
		table.WithHeight(4),
	)

	sp := spinner.New()
	sp.Spinner = spinner.MiniDot
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color(styles.ColorCyan))

	return &Dashboard{
		Session:  s,
		table:    t,
		progress: progress.New(progress.WithSolidFill(styles.ColorAccent)),
		spinner:  sp,
		theme:    styles.DefaultTheme(),
	}
}

// SetAlbumSlug stores the URL slug for creating album subfolders.
func (d *Dashboard) SetAlbumSlug(slug string) {
	d.AlbumSlug = slug
}

// SetSourceURL records the URL a single-URL run scraped, so failed items
// can be reported with the source they came from (retry pairs).
func (d *Dashboard) SetSourceURL(url string) {
	d.SourceURL = url
}

// SetRetryWriter installs the CLI callback used by the summary screen.
func (d *Dashboard) SetRetryWriter(fn func([]model.FailedItem) (string, error)) {
	d.writeRetryList = fn
}

func (d *Dashboard) Init() tea.Cmd {
	if d.BatchMode {
		return tea.Batch(d.spinner.Tick, d.runTasks(d.Start()))
	}
	return tea.Batch(d.spinner.Tick, d.runTasks(d.Start()),
		tea.Tick(500*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg(t) }))
}

// --- Messages ---

type progressMsg model.ProgressEvent
type refreshTickMsg struct{}
type downloadDoneMsg struct {
	key string
	err error
}
type statusClearMsg struct{ seq int }
type tickMsg time.Time
type scrapeResultMsg struct {
	items []model.MediaItem
	err   error
}
type batchScrapeMsg struct {
	album model.Album
	idx   int
	items []model.MediaItem
	err   error
}

// runTasks turns session tasks into commands that run them off the update
// loop and report back as messages.
func (d *Dashboard) runTasks(tasks []session.Task) tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(tasks))
	for _, t := range tasks {
		cmds = append(cmds, d.taskCmd(t))
	}
	return tea.Batch(cmds...)
}

func (d *Dashboard) taskCmd(t session.Task) tea.Cmd {
	return func() tea.Msg {
		r := d.Exec(t)
		switch {
		case t.Kind == session.TaskDownload:
			return downloadDoneMsg{key: t.Item.Key(), err: r.Err}
		case t.Batch:
			return batchScrapeMsg{album: t.Album, items: r.Items, err: r.Err}
		default:
			return scrapeResultMsg{items: r.Items, err: r.Err}
		}
	}
}

// --- Update ---

func (d *Dashboard) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	defer d.syncTable()
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		d.width, d.height, d.ready = msg.Width, msg.Height, true
		d.table.SetWidth(max(0, msg.Width-4))
		d.table.SetColumns([]table.Column{
			{Title: "", Width: 2},
			{Title: "FILE", Width: max(1, msg.Width-52)},
			{Title: "STATUS", Width: 12},
			{Title: "PROGRESS", Width: 26},
		})
		d.fitTableHeight()
		bw := msg.Width - 8
		if bw > 80 {
			bw = 80
		}
		if bw > 0 {
			d.progress.Width = bw
		}
		return d, nil

	case spinner.TickMsg:
		if !d.Active() {
			return d, nil
		}
		var cmd tea.Cmd
		d.spinner, cmd = d.spinner.Update(msg)
		return d, cmd

	case tickMsg:
		if !d.Active() {
			// All downloads are done; stop re-arming instead of
			// redrawing forever (the refreshTickMsg branch is gated the
			// same way).
			return d, nil
		}
		d.Tick()
		// The fast refresh tick redraws rows on its own while it runs.
		if !d.refreshTickActive {
			d.refreshRows()
		}
		return d, tea.Tick(500*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg(t) })

	case scrapeResultMsg:
		return d.withStatusClear(d.handleScrapeResult(msg))

	case batchScrapeMsg:
		return d.withStatusClear(d.handleBatchScrape(msg))

	case tea.KeyMsg:
		if d.Phase == session.PhaseSummary {
			return d.handleSummaryKey(msg)
		}
		if d.searching {
			return d.handleSearchKey(msg)
		}
		switch msg.String() {
		case "/":
			d.searching = true
			d.searchQuery = ""
			return d, nil
		case "n":
			if d.searchQuery != "" {
				d.jumpToMatch()
			}
			return d, nil
		case "F":
			d.inspectMsg = ""
			d.jumpIf(session.IsFailed)
			return d, nil
		case "C":
			d.cancelAll()
			return d, nil
		case "f":
			d.CycleFilter()
			d.rebuildVisible()
			d.refreshRows()
			d.fitTableHeight()
			return d, nil
		case tea.KeyTab.String():
			d.toggleFoldAtCursor()
			return d, nil
		case "R":
			d.inspectMsg = ""
			return d, d.retryAllFailed()
		case "q", "ctrl+c", "esc":
			return d.quit()
		case "up", "k":
			d.inspectMsg = ""
			d.moveUpSkippingHeaders()
			return d, nil
		case "down", "j":
			d.inspectMsg = ""
			d.moveDownSkippingHeaders()
			return d, nil
		case "c":
			d.inspectMsg = ""
			d.cancelSelected()
			return d, nil
		case "e":
			d.showErrors = !d.showErrors
			return d, nil
		case "enter":
			d.inspectSelected()
			return d, nil
		case "r":
			d.inspectMsg = ""
			return d, d.retrySelected()
		}
	case tea.MouseMsg:
		switch msg.Button {
		case tea.MouseButtonWheelUp:
			d.moveUpSkippingHeaders()
		case tea.MouseButtonWheelDown:
			d.moveDownSkippingHeaders()
		}
		return d, nil

	case progressMsg:
		d.ApplyProgress(model.ProgressEvent(msg))
		d.rebuildVisible()
		d.refreshRows()
		return d, nil

	case refreshTickMsg:
		keepTicking := d.Refresh()
		d.rebuildVisible()
		d.refreshRows()
		if keepTicking {
			return d, tea.Tick(time.Second/15, func(t time.Time) tea.Msg { return refreshTickMsg{} })
		}
		d.refreshTickActive = false
		return d, nil

	case downloadDoneMsg:
		d.applyDone(msg)
		d.rebuildVisible()
		d.refreshRows()
		if d.Status != "" {
			return d, d.statusClearCmd()
		}
		return d, nil
	case statusClearMsg:
		if msg.seq == d.StatusSeq {
			d.Status = ""
		}
		return d, nil
	}

	var cmd tea.Cmd
	d.table, cmd = d.table.Update(msg)
	return d, cmd
}

func (d *Dashboard) withStatusClear(m tea.Model, cmd tea.Cmd) (tea.Model, tea.Cmd) {
	if d.Status != "" {
		return m, tea.Batch(cmd, d.statusClearCmd())
	}
	return m, cmd
}

// handleSummaryKey accepts only the summary's own keys; the table behind it is hidden.
func (d *Dashboard) handleSummaryKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		d.DismissSummary()
	case "R":
		if len(d.Failures()) > 0 {
			return d, d.retryAllFailed()
		}
	case "L":
		if d.writeRetryList == nil {
			return d, nil
		}
		if path, err := d.writeRetryList(d.Failures()); err != nil {
			d.SetStatus("retry list: %v", err)
		} else {
			d.SetStatus("retry list written: %s", path)
		}
		return d, d.statusClearCmd()
	case "q", "ctrl+c", "esc":
		return d.quit()
	}
	return d, nil
}

func (d *Dashboard) quit() (tea.Model, tea.Cmd) {
	d.quitting = true
	d.Shutdown()
	return d, tea.Quit
}
