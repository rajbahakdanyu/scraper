// internal/ui/dashboard/dashboard.go
package dashboard

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/rajbahakdanyu/scraper/internal/model"
	"github.com/rajbahakdanyu/scraper/internal/ui/styles"
)

type phase int

const (
	phaseLoading phase = iota
	phaseDownload
	phaseDone
	phaseSummary
)

type listFilter int

const (
	filterAll listFilter = iota
	filterFailed
	filterActive
	filterPending
)

type itemState struct {
	item      model.MediaItem
	status    model.DownloadStatus
	bytes     int64
	total     int64
	speed     float64
	started   time.Time
	dest      string
	isHeader  bool
	albumName string
	albumSite string
	sourceURL string
	lastErr   error
}

type Dashboard struct {
	table    table.Model
	progress progress.Model
	spinner  spinner.Model
	theme    styles.Theme

	states        []itemState
	stateIndex    map[string]int
	indexedStates int
	rows          []table.Row
	visible       []int
	visibleReady  bool
	downloader    model.Downloader
	dest          string

	width  int
	height int
	ready  bool

	quitting  bool
	status    string
	statusSeq int
	site      string
	phase     phase

	scraperFn  func(context.Context) ([]model.MediaItem, error)
	loadingErr string

	albumSlug         string
	sourceURL         string
	errorLog          []string
	showErrors        bool
	searching         bool
	searchQuery       string
	progressMu        sync.Mutex
	pending           map[string]model.ProgressEvent
	refreshTickActive bool

	batchMode      bool
	batchURLs      []model.Album
	batchIdx       int
	batchRemaining int
	scrapeErrs     []error
	scrapeCtx      context.Context
	cancelScrape   context.CancelFunc

	inspectMsg       string
	folded           map[int]bool
	manuallyUnfolded map[int]bool
	filter           listFilter
	writeRetryList   func([]model.FailedItem) (string, error)
}

// Futuristic logo — clean, minimal
const logo = "◢◤  SCRAPER  ◢◤"

func New(scraperFn func(context.Context) ([]model.MediaItem, error), dl model.Downloader, dest string, site string) *Dashboard {
	d := newDashboard(dl, dest, site)
	d.scraperFn = scraperFn
	return d
}

// newDashboard builds the state shared by single-URL and batch runs.
func newDashboard(dl model.Downloader, dest, site string) *Dashboard {
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

	d := &Dashboard{
		table:      t,
		progress:   progress.New(progress.WithSolidFill(styles.ColorAccent)),
		spinner:    sp,
		theme:      styles.DefaultTheme(),
		downloader: dl,
		dest:       dest,
		site:       site,
		phase:      phaseLoading,
	}
	d.scrapeCtx, d.cancelScrape = context.WithCancel(context.Background())
	return d
}

// SetAlbumSlug stores the URL slug for creating album subfolders.
func (d *Dashboard) SetAlbumSlug(slug string) {
	d.albumSlug = slug
}

// SetSourceURL records the URL a single-URL run scraped, so failed items
// can be reported with the source they came from (retry pairs).
func (d *Dashboard) SetSourceURL(url string) {
	d.sourceURL = url
}

// SetRetryWriter installs the CLI callback used by the summary screen.
func (d *Dashboard) SetRetryWriter(fn func([]model.FailedItem) (string, error)) {
	d.writeRetryList = fn
}

// NewBatch creates a dashboard for batch mode. Items are appended
// incrementally as each album is scraped via the scrape-chain in Init.
func NewBatch(dl model.Downloader, destRoot string, urls []model.Album) *Dashboard {
	siteLabel := fmt.Sprintf("%d albums", len(urls))
	if len(urls) == 1 {
		siteLabel = urls[0].Site
	}

	d := newDashboard(dl, destRoot, siteLabel)
	d.batchMode = true
	d.batchURLs = urls
	d.batchRemaining = len(urls)
	return d
}

func (d *Dashboard) Init() tea.Cmd {
	if d.batchMode {
		return tea.Batch(d.spinner.Tick, d.startNextScrape())
	}
	return tea.Batch(d.spinner.Tick, d.startScrape(),
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

func (d *Dashboard) startScrape() tea.Cmd {
	return func() tea.Msg {
		items, err := d.scraperFn(d.scrapeCtx)
		return scrapeResultMsg{items: items, err: err}
	}
}

func (d *Dashboard) startNextScrape() tea.Cmd {
	return func() tea.Msg {
		if d.batchIdx >= len(d.batchURLs) {
			return batchScrapeMsg{idx: -1}
		}
		album := d.batchURLs[d.batchIdx]
		items, err := album.Scraper.Parse(d.scrapeCtx, album.URL)
		return batchScrapeMsg{album: album, idx: d.batchIdx, items: items, err: err}
	}
}

func startDownload(dl model.Downloader, item model.MediaItem, dest string) tea.Cmd {
	return func() tea.Msg {
		_, err := dl.Download(item, dest)
		return downloadDoneMsg{key: item.Key(), err: err}
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
		if !d.spinnerActive() {
			return d, nil
		}
		var cmd tea.Cmd
		d.spinner, cmd = d.spinner.Update(msg)
		return d, cmd

	case tickMsg:
		if !d.spinnerActive() {
			// All downloads are done; stop re-arming instead of
			// redrawing forever (the refreshTickMsg branch is gated the
			// same way).
			return d, nil
		}
		// Recalculate speed from elapsed time on each tick
		for i := range d.states {
			if d.states[i].status == model.StatusDownloading && !d.states[i].started.IsZero() {
				elapsed := time.Since(d.states[i].started).Seconds()
				if elapsed > 0 && d.states[i].bytes > 0 {
					d.states[i].speed = float64(d.states[i].bytes) / elapsed
				}
			}
		}
		// The fast refresh tick redraws rows on its own while it runs.
		if !d.refreshTickActive {
			d.refreshRows()
		}
		return d, tea.Tick(500*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg(t) })

	case scrapeResultMsg:
		m, cmd := d.handleScrapeResult(msg)
		if d.status != "" {
			return m, tea.Batch(cmd, d.statusClearCmd())
		}
		return m, cmd

	case batchScrapeMsg:
		m, cmd := d.handleBatchScrape(msg)
		if d.status != "" {
			return m, tea.Batch(cmd, d.statusClearCmd())
		}
		return m, cmd

	case tea.KeyMsg:
		if d.phase == phaseSummary {
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
			d.jumpIf(func(st itemState) bool { return st.status == model.StatusFailed })
			return d, nil
		case "C":
			d.cancelAll()
			return d, nil
		case "f":
			d.filter = (d.filter + 1) % 4
			d.rebuildVisible()
			d.refreshRows()
			d.fitTableHeight()
			return d, nil
		case tea.KeyTab.String():
			d.toggleFoldAtCursor()
			return d, nil
		case "R":
			d.inspectMsg = ""
			return d, d.resumeAfter(d.retryAllFailed())
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
			return d, d.resumeAfter(d.retrySelected())
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
		d.applyProgress(model.ProgressEvent(msg))
		d.rebuildVisible()
		d.refreshRows()
		return d, nil

	case refreshTickMsg:
		d.applyPendingProgress()
		d.autoFoldCompletedAlbums()
		d.rebuildVisible()
		d.refreshRows()
		if d.phase == phaseDownload && d.batchRemaining == 0 && len(d.states) > 0 && d.settled() {
			d.phase = phaseSummary
			d.refreshTickActive = false
			return d, nil
		}
		if d.spinnerActive() || d.hasPending() {
			return d, tea.Tick(time.Second/15, func(t time.Time) tea.Msg { return refreshTickMsg{} })
		}
		d.refreshTickActive = false
		return d, nil

	case downloadDoneMsg:
		d.applyDone(msg)
		d.rebuildVisible()
		d.refreshRows()
		if d.status != "" {
			return d, d.statusClearCmd()
		}
		return d, nil
	case statusClearMsg:
		if msg.seq == d.statusSeq {
			d.status = ""
		}
		return d, nil
	}

	var cmd tea.Cmd
	d.table, cmd = d.table.Update(msg)
	return d, cmd
}

// handleSummaryKey accepts only the summary's own keys; the table behind it is hidden.
func (d *Dashboard) handleSummaryKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		d.phase = phaseDone
	case "R":
		if len(d.Failures()) > 0 {
			return d, d.resumeAfter(d.retryAllFailed())
		}
	case "L":
		if d.writeRetryList == nil {
			return d, nil
		}
		if path, err := d.writeRetryList(d.Failures()); err != nil {
			d.setStatus("retry list: %v", err)
		} else {
			d.setStatus("retry list written: %s", path)
		}
		return d, d.statusClearCmd()
	case "q", "ctrl+c", "esc":
		return d.quit()
	}
	return d, nil
}

// resumeAfter re-arms summary detection when a retry restarts a settled run.
func (d *Dashboard) resumeAfter(cmd tea.Cmd) tea.Cmd {
	if cmd != nil {
		d.phase = phaseDownload
	}
	return cmd
}

func (d *Dashboard) quit() (tea.Model, tea.Cmd) {
	d.quitting = true
	if d.cancelScrape != nil {
		d.cancelScrape()
	}
	if d.downloader != nil {
		// Closing the progress channel also ends drainProgress.
		d.downloader.Shutdown()
	}
	return d, tea.Quit
}
