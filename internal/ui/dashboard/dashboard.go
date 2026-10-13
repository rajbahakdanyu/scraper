// internal/ui/dashboard/dashboard.go
package dashboard

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/table"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/rajbahakdanyu/scraper/internal/fileutil"
	"github.com/rajbahakdanyu/scraper/internal/model"
	"github.com/rajbahakdanyu/scraper/internal/ui/styles"
)

type Downloader interface {
	Prepare(item model.MediaItem)
	Download(item model.MediaItem, dest string) (string, error)
	Cancel(item model.MediaItem)
	// Shutdown cancels every in-flight download and closes the progress
	// channel once they have unwound.
	Shutdown()
	Progress() <-chan model.ProgressEvent
}

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

// DashAlbumURL carries the metadata cmd/root.go passes to the dashboard for
// each URL in a batch: the orchestrator scrapes them incrementally and the
// dashboard appends items as each album arrives.
type DashAlbumURL struct {
	Site    string
	URL     string
	Slug    string
	Dest    string
	Scraper model.Scraper
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
	dirtyRows     map[int]struct{}
	downloader    Downloader
	dest          string

	width  int
	height int
	ready  bool

	quitting    bool
	status      string
	statusSeq   int
	autoClearIn int
	site        string
	phase       phase

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
	batchURLs      []DashAlbumURL
	batchIdx       int
	batchRemaining int
	scrapeCtx      context.Context
	cancelScrape   context.CancelFunc

	inspectMsg       string
	folded           map[int]bool
	manuallyUnfolded map[int]bool
	filter           listFilter
	writeRetryList   func([]FailedItem) (string, error)
}

// Futuristic logo — clean, minimal
const logo = "◢◤  SCRAPER  ◢◤"

func New(scraperFn func(context.Context) ([]model.MediaItem, error), dl Downloader, dest string, site string) *Dashboard {
	columns := []table.Column{
		{Title: "", Width: 2},
		{Title: "FILE", Width: 34},
		{Title: "STATUS", Width: 12},
		{Title: "PROGRESS", Width: 26},
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
func (d *Dashboard) SetRetryWriter(fn func([]FailedItem) (string, error)) { d.writeRetryList = fn }

// NewBatch creates a dashboard for batch mode. Items are appended
// incrementally as each album is scraped via the scrape-chain in Init.
func NewBatch(dl Downloader, destRoot string, urls []DashAlbumURL) *Dashboard {
	columns := []table.Column{
		{Title: "", Width: 2},
		{Title: "FILE", Width: 34},
		{Title: "STATUS", Width: 12},
		{Title: "PROGRESS", Width: 26},
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithFocused(true),
		table.WithHeight(4),
	)

	sp := spinner.New()
	sp.Spinner = spinner.MiniDot
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color(styles.ColorCyan))

	siteLabel := fmt.Sprintf("%d albums", len(urls))
	if len(urls) == 1 {
		siteLabel = urls[0].Site
	}

	d := &Dashboard{
		table:          t,
		progress:       progress.New(progress.WithSolidFill(styles.ColorAccent)),
		spinner:        sp,
		theme:          styles.DefaultTheme(),
		downloader:     dl,
		dest:           destRoot,
		site:           siteLabel,
		phase:          phaseLoading,
		batchMode:      true,
		batchURLs:      urls,
		batchRemaining: len(urls),
	}
	d.scrapeCtx, d.cancelScrape = context.WithCancel(context.Background())
	return d
}

func (d *Dashboard) Init() tea.Cmd {
	if d.batchMode {
		return tea.Batch(tea.EnterAltScreen, d.spinner.Tick, d.startNextScrape())
	}
	return tea.Batch(tea.EnterAltScreen, d.spinner.Tick, d.startScrape(),
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
	album DashAlbumURL
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

func startDownload(dl Downloader, item model.MediaItem, dest string) tea.Cmd {
	return func() tea.Msg {
		_, err := dl.Download(item, dest)
		return downloadDoneMsg{key: item.Key(), err: err}
	}
}

// --- Update ---

func (d *Dashboard) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	defer d.fitTableHeight()
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
		d.refreshRows()
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
		case "L":
			if d.phase == phaseSummary && d.writeRetryList != nil {
				if path, err := d.writeRetryList(d.Failures()); err != nil {
					d.setStatus("retry list: %v", err)
				} else {
					d.setStatus("retry list written: %s", path)
				}
				return d, d.statusClearCmd()
			}
			return d, nil
		case "R":
			d.inspectMsg = ""
			if d.phase == phaseSummary && len(d.Failures()) > 0 {
				d.phase = phaseDownload
			} else if d.phase == phaseSummary {
				return d, nil
			}
			return d, d.retryAllFailed()
		case "q", "ctrl+c", "esc":
			d.quitting = true
			if d.cancelScrape != nil {
				d.cancelScrape()
			}
			if d.downloader != nil {
				// Cancel in-flight downloads and close the progress
				// channel (drainProgress's range loop then exits).
				d.downloader.Shutdown()
			}
			return d, tea.Quit
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
		switch msg.Type {
		case tea.MouseWheelUp:
			d.moveUpSkippingHeaders()
		case tea.MouseWheelDown:
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
		if msg.seq == d.statusSeq && msg.seq == d.autoClearIn {
			d.status = ""
		}
		return d, nil
	}

	var cmd tea.Cmd
	d.table, cmd = d.table.Update(msg)
	return d, cmd
}

func (d *Dashboard) handleScrapeResult(msg scrapeResultMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil && len(msg.items) == 0 {
		d.loadingErr = msg.err.Error()
		d.appendError(msg.err.Error())
		d.phase = phaseDone
		return d, nil
	}
	if len(msg.items) == 0 {
		d.loadingErr = "no media items found"
		d.phase = phaseDone
		return d, nil
	}
	if msg.err != nil {
		d.setStatus("warning: %v", msg.err)
		d.appendError(msg.err.Error())
	}

	d.states = make([]itemState, len(msg.items))
	for i := range msg.items {
		model.AssignIDs(msg.items[i:i+1], fmt.Sprintf("%d:", i))
		it := msg.items[i]
		d.states[i] = itemState{item: it, status: model.StatusPending, total: it.Size, sourceURL: d.sourceURL}
	}

	// Album (multiple files): create a subfolder from the URL slug
	if len(msg.items) > 1 && d.albumSlug != "" {
		albumDest, err := fileutil.SafeJoin(d.dest, d.albumSlug)
		if err != nil {
			d.states = nil
			d.loadingErr = err.Error()
			d.phase = phaseDone
			return d, nil
		}
		if err := os.MkdirAll(albumDest, 0750); err != nil {
			d.states = nil
			d.loadingErr = fmt.Sprintf("create album directory: %v", err)
			d.phase = phaseDone
			return d, nil
		}
		fi, err := os.Lstat(albumDest)
		if err != nil {
			d.states = nil
			d.loadingErr = err.Error()
			d.phase = phaseDone
			return d, nil
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			d.states = nil
			d.loadingErr = fmt.Sprintf("refusing to use symlinked output directory: %s", albumDest)
			d.phase = phaseDone
			return d, nil
		}
		d.dest = albumDest
	}

	d.phase = phaseDownload
	d.rebuildVisible()
	d.refreshRows()
	d.fitTableHeight()

	var cmds []tea.Cmd
	if d.downloader != nil {
		go d.drainProgress()
		cmds = append(cmds, d.startAllDownloads(), d.ensureRefreshTicker())
	}
	return d, tea.Batch(cmds...)
}

// drainProgress reads from the downloader's progress channel in a loop
// and populates the pending map. It exits when the downloader closes the
// channel (Shutdown). Progress is applied to the UI by the refresh ticker.
func (d *Dashboard) drainProgress() {
	if d.downloader == nil {
		return
	}
	ch := d.downloader.Progress()
	for evt := range ch {
		d.progressMu.Lock()
		if d.pending == nil {
			d.pending = make(map[string]model.ProgressEvent)
		}
		d.pending[evt.Item.Key()] = evt
		d.progressMu.Unlock()
	}
}

func (d *Dashboard) ensureRefreshTicker() tea.Cmd {
	if d.refreshTickActive {
		return nil
	}
	d.refreshTickActive = true
	return func() tea.Msg { return refreshTickMsg{} }
}

func (d *Dashboard) hasPending() bool {
	d.progressMu.Lock()
	defer d.progressMu.Unlock()
	return len(d.pending) > 0
}

func (d *Dashboard) applyPendingProgress() {
	d.progressMu.Lock()
	pending := d.pending
	d.pending = make(map[string]model.ProgressEvent)
	d.progressMu.Unlock()
	if len(pending) == 0 {
		return
	}
	for _, evt := range pending {
		d.applyProgress(evt)
	}
}

func (d *Dashboard) handleBatchScrape(msg batchScrapeMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil && len(msg.items) == 0 {
		d.loadingErr = fmt.Sprintf("%s: %v", msg.album.URL, msg.err)
		d.appendError(d.loadingErr)
		d.batchRemaining--
		d.batchIdx++
		if d.batchRemaining == 0 && len(d.states) == 0 {
			d.phase = phaseDone
			return d, nil
		}
		return d, d.startNextScrape()
	}

	if msg.err != nil {
		d.setStatus("warning: %v", msg.err)
		d.appendError(fmt.Sprintf("%s: %v", msg.album.URL, msg.err))
	}

	d.batchRemaining--
	d.batchIdx++

	if len(msg.items) == 0 {
		if msg.err != nil {
			d.setStatus("warning: %v", msg.err)
		}
		d.refreshRows()
		if d.batchRemaining > 0 {
			return d, d.startNextScrape()
		}
		if len(d.states) == 0 {
			d.phase = phaseDone
		}
		return d, nil
	}

	wasFirstAlbum := d.phase == phaseLoading

	if err := os.MkdirAll(msg.album.Dest, 0750); err != nil {
		d.loadingErr = fmt.Sprintf("create album directory: %v", err)
		d.phase = phaseDone
		return d, nil
	}
	fi, err := os.Lstat(msg.album.Dest)
	if err != nil {
		d.loadingErr = err.Error()
		d.phase = phaseDone
		return d, nil
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		d.loadingErr = fmt.Sprintf("refusing to use symlinked output directory: %s", msg.album.Dest)
		d.phase = phaseDone
		return d, nil
	}

	baseIdx := len(d.states)
	headerState := itemState{
		isHeader:  true,
		albumName: msg.album.Slug,
		albumSite: msg.album.Site,
	}
	d.states = append(d.states, headerState)

	for i := range msg.items {
		model.AssignIDs(msg.items[i:i+1], fmt.Sprintf("%d:%d:", d.batchIdx-1, i))
		it := msg.items[i]
		st := itemState{
			item:      it,
			total:     it.Size,
			dest:      msg.album.Dest,
			sourceURL: msg.album.URL,
		}
		if it.URL == "" {
			st.status = model.StatusCompleted
		} else {
			st.status = model.StatusPending
		}
		d.states = append(d.states, st)
	}

	if wasFirstAlbum {
		d.phase = phaseDownload
		d.rebuildVisible()
		d.refreshRows()
		d.fitTableHeight()

		var cmds []tea.Cmd
		if d.downloader != nil {
			go d.drainProgress()
			cmds = append(cmds, d.startAllDownloads(), d.ensureRefreshTicker())
		}
		if d.batchRemaining > 0 {
			cmds = append(cmds, d.startNextScrape())
		}
		return d, tea.Batch(cmds...)
	}

	// Subsequent album: items were appended. Start downloads only for the
	// new items, and kick off the next scrape if applicable.
	d.rebuildVisible()
	d.refreshRows()
	d.fitTableHeight()
	totalRows := len(d.states)

	var cmds []tea.Cmd
	if d.downloader != nil {
		for i := baseIdx + 1; i < totalRows; i++ {
			if d.states[i].item.URL == "" {
				continue
			}
			d.downloader.Prepare(d.states[i].item)
			cmds = append(cmds, startDownload(d.downloader, d.states[i].item, d.states[i].dest))
		}
	}
	if d.batchRemaining > 0 {
		cmds = append(cmds, d.startNextScrape())
	}
	return d, tea.Batch(cmds...)
}

func (d *Dashboard) moveUpSkippingHeaders() {
	if d.visibleReady && len(d.visible) == 0 {
		return
	}
	if len(d.visible) > 0 {
		idx := d.table.Cursor() - 1
		for idx >= 0 && d.skipHeader(idx) {
			idx--
		}
		if idx < 0 {
			for idx = 0; idx < len(d.visible) && d.skipHeader(idx); idx++ {
			}
		}
		if idx >= 0 && idx < len(d.visible) {
			d.selectRow(idx)
		}
		return
	}
	d.table.MoveUp(1)
	for idx := d.table.Cursor(); idx > 0 && idx < len(d.states) && d.states[idx].isHeader; {
		d.table.MoveUp(1)
		idx = d.table.Cursor()
	}
	// The scan can stop at row 0 when that row is a header (there is no
	// non-header row above it). Park on the first non-header row instead
	// of leaving the cursor on the header.
	if idx := d.table.Cursor(); idx == 0 && len(d.states) > 0 && d.states[0].isHeader {
		for i := 1; i < len(d.states); i++ {
			if !d.states[i].isHeader {
				d.table.SetCursor(i)
				break
			}
		}
	}
}

func (d *Dashboard) moveDownSkippingHeaders() {
	if d.visibleReady && len(d.visible) == 0 {
		return
	}
	if len(d.visible) > 0 {
		idx := d.table.Cursor() + 1
		for idx < len(d.visible) && d.skipHeader(idx) {
			idx++
		}
		if idx >= len(d.visible) {
			idx = d.table.Cursor()
		}
		if idx >= 0 && idx < len(d.visible) {
			d.selectRow(idx)
		}
		return
	}
	d.table.MoveDown(1)
	idx := d.table.Cursor()
	for idx >= 0 && idx < len(d.states) && d.states[idx].isHeader {
		d.table.MoveDown(1)
		idx = d.table.Cursor()
	}
}

// --- Batch ---

// cancelSelected cancels the currently-highlighted item's download, if
// it's still in progress.
func (d *Dashboard) cancelSelected() {
	if d.downloader == nil {
		return
	}
	idx := d.stateAt(d.table.Cursor())
	if idx < 0 || idx >= len(d.states) {
		return
	}
	if d.states[idx].isHeader {
		return
	}
	if d.states[idx].status != model.StatusPending && d.states[idx].status != model.StatusDownloading {
		return
	}
	d.downloader.Cancel(d.states[idx].item)
	d.states[idx].status = model.StatusCancelled
	d.rebuildVisible()
	d.refreshRows()
}

func (d *Dashboard) cancelAll() {
	if d.downloader == nil {
		return
	}
	for i := range d.states {
		st := &d.states[i]
		if st.isHeader || (st.status != model.StatusPending && st.status != model.StatusDownloading) {
			continue
		}
		d.downloader.Cancel(st.item)
		st.status = model.StatusCancelled
	}
	d.rebuildVisible()
	d.refreshRows()
}

func (d *Dashboard) inspectSelected() {
	d.inspectMsg = ""
	idx := d.stateAt(d.table.Cursor())
	if idx < 0 || idx >= len(d.states) {
		return
	}
	if d.states[idx].isHeader {
		return
	}
	if d.states[idx].status != model.StatusFailed {
		return
	}
	if d.states[idx].lastErr != nil {
		d.inspectMsg = d.states[idx].lastErr.Error()
	}
}

// FailedItem pairs a media item with the error that ended its download and
// the URL it was scraped from.
type FailedItem struct {
	Item      model.MediaItem
	Err       error
	SourceURL string
}

// Failures returns the items that ended in the failed state, in display
// order. The CLI uses it to write a retry list after the TUI exits.
func (d *Dashboard) Failures() []FailedItem {
	var out []FailedItem
	for i := range d.states {
		if d.states[i].isHeader || d.states[i].status != model.StatusFailed {
			continue
		}
		out = append(out, FailedItem{
			Item:      d.states[i].item,
			Err:       d.states[i].lastErr,
			SourceURL: d.states[i].sourceURL,
		})
	}
	return out
}

// handleSearchKey edits the search query while `/` search is active.
func (d *Dashboard) handleSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		d.searching = false
		d.searchQuery = ""
	case "enter":
		d.searching = false
		d.jumpToMatch()
	case "backspace":
		if runes := []rune(d.searchQuery); len(runes) > 0 {
			d.searchQuery = string(runes[:len(runes)-1])
		}
	default:
		if len(msg.Runes) > 0 {
			d.searchQuery += string(msg.Runes)
		}
	}
	return d, nil
}

// jumpToMatch moves the cursor to the next item whose title contains the
// search query, wrapping around and skipping headers.
func (d *Dashboard) jumpToMatch() {
	if d.searchQuery == "" || len(d.states) == 0 {
		return
	}
	query := strings.ToLower(d.searchQuery)
	d.jumpIf(func(st itemState) bool { return strings.Contains(strings.ToLower(model.TitleOf(st.item)), query) })
}

func (d *Dashboard) jumpIf(match func(itemState) bool) {
	if len(d.visible) == 0 {
		return
	}
	start := d.table.Cursor()
	for step := 1; step <= len(d.visible); step++ {
		row := (start + step) % len(d.visible)
		idx := d.stateAt(row)
		st := d.states[idx]
		if !st.isHeader && match(st) {
			d.selectRow(row)
			return
		}
	}
}

// retryAllFailed resets every failed item and starts fresh downloads for
// them, so a batch's failures can be retried without hunting for each row.
func (d *Dashboard) retryAllFailed() tea.Cmd {
	if d.downloader == nil {
		return nil
	}
	var cmds []tea.Cmd
	for i := range d.states {
		if d.states[i].isHeader || d.states[i].status != model.StatusFailed {
			continue
		}
		dest := d.states[i].dest
		if dest == "" {
			dest = d.dest
		}
		d.states[i].status = model.StatusPending
		d.states[i].bytes = 0
		d.states[i].total = d.states[i].item.Size
		d.states[i].speed = 0
		d.states[i].started = time.Time{}
		d.states[i].lastErr = nil
		d.downloader.Prepare(d.states[i].item)
		cmds = append(cmds, startDownload(d.downloader, d.states[i].item, dest))
	}
	if len(cmds) == 0 {
		return nil
	}
	d.rebuildVisible()
	d.refreshRows()
	return tea.Batch(append(cmds, d.ensureRefreshTicker())...)
}

// retrySelected resets the selected failed item back to pending and
// kicks off a fresh download attempt.
func (d *Dashboard) retrySelected() tea.Cmd {
	if d.downloader == nil {
		return nil
	}
	idx := d.stateAt(d.table.Cursor())
	if idx < 0 || idx >= len(d.states) {
		return nil
	}
	if d.states[idx].isHeader {
		return nil
	}
	if d.states[idx].status != model.StatusFailed {
		return nil
	}
	dest := d.states[idx].dest
	if dest == "" {
		dest = d.dest
	}
	d.states[idx].status = model.StatusPending
	d.states[idx].bytes = 0
	d.states[idx].total = d.states[idx].item.Size
	d.states[idx].speed = 0
	d.states[idx].started = time.Time{}
	d.states[idx].lastErr = nil
	d.downloader.Prepare(d.states[idx].item)
	d.rebuildVisible()
	d.refreshRows()
	return tea.Batch(startDownload(d.downloader, d.states[idx].item, dest), d.ensureRefreshTicker())
}

func (d *Dashboard) startAllDownloads() tea.Cmd {
	if d.downloader == nil {
		return nil
	}
	var cmds []tea.Cmd
	for i := range d.states {
		if d.states[i].isHeader {
			continue
		}
		if d.states[i].item.URL == "" {
			continue
		}
		d.downloader.Prepare(d.states[i].item)
		dest := d.states[i].dest
		if dest == "" {
			dest = d.dest
		}
		cmds = append(cmds, startDownload(d.downloader, d.states[i].item, dest))
	}
	d.refreshRows()
	return tea.Batch(cmds...)
}

func (d *Dashboard) applyProgress(evt model.ProgressEvent) {
	d.ensureStateIndex()
	i, ok := d.stateIndex[evt.Item.Key()]
	if !ok {
		return
	}
	// Terminal states are authoritative: a progress event that was
	// buffered before the item finished (cancelled, completed, failed)
	// must not resurrect it back to an active state.
	switch d.states[i].status {
	case model.StatusCompleted, model.StatusFailed, model.StatusCancelled:
		return
	}
	// Terminal status values (Failed, Cancelled) must only come from
	// applyDone, not from buffered progress events — a stale
	// StatusFailed event queued before a retry would otherwise
	// corrupt the retried item.
	switch evt.Status {
	case model.StatusFailed, model.StatusCancelled:
		return
	case model.StatusCompleted:
		evt.Status = model.StatusDownloading
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
	d.markDirty(i)
}

func (d *Dashboard) applyDone(msg downloadDoneMsg) {
	d.ensureStateIndex()
	i, ok := d.stateIndex[msg.key]
	if !ok {
		return
	}
	switch {
	case msg.err == nil:
		d.states[i].status = model.StatusCompleted
		d.states[i].lastErr = nil
		if d.states[i].total > 0 {
			d.states[i].bytes = d.states[i].total
		}
	case d.states[i].status == model.StatusCancelled:
		// Already marked cancelled via the progress channel; a
		// cancelled download also returns an error here, but that's
		// not a failure worth downgrading the status for.
	default:
		d.states[i].status = model.StatusFailed
		d.states[i].lastErr = msg.err
		d.setStatus("✗ %s: %v", model.TitleOf(d.states[i].item), msg.err)
	}
	d.markDirty(i)
}

// --- Rendering ---

func (d *Dashboard) refreshRows() {
	if !d.visibleReady {
		d.rebuildVisible()
	}
	if len(d.rows) != len(d.visible) {
		d.rows = make([]table.Row, len(d.visible))
		d.dirtyRows = make(map[int]struct{}, len(d.visible))
		for row := range d.visible {
			d.dirtyRows[row] = struct{}{}
		}
	}
	for row := range d.dirtyRows {
		i := d.stateAt(row)
		st := d.states[i]
		if st.isHeader {
			var done, failed int
			for j := i + 1; j < len(d.states); j++ {
				if d.states[j].isHeader {
					break
				}
				switch d.states[j].status {
				case model.StatusCompleted:
					done++
				case model.StatusFailed:
					failed++
				}
			}
			total := 0
			for j := i + 1; j < len(d.states); j++ {
				if d.states[j].isHeader {
					break
				}
				total++
			}
			pct := 0
			if total > 0 {
				pct = 100 * done / total
			}
			chevron := "▾"
			if d.folded[i] {
				chevron = "▸"
			}
			headerText := fmt.Sprintf("%s %s  (%s)", chevron, st.albumName, st.albumSite)
			progressText := fmt.Sprintf("%s %d%%", renderBar(int64(done), int64(total), 10), pct)
			if failed > 0 {
				progressText += fmt.Sprintf(" (%d failed)", failed)
			}
			styled := d.theme.Cyan.Bold(true).Render(headerText)
			d.rows[row] = table.Row{"", styled, fmt.Sprintf("%d/%d", done, total), progressText}
			continue
		}

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

		progressText := miniBar(st)
		if st.status == model.StatusDownloading && st.speed > 0 {
			progressText += fmt.Sprintf("  %s/s", HumanizeBytes(int64(st.speed)))
		}
		if st.status == model.StatusPending {
			if q := d.pendingQueueNumber(i); q > 0 {
				progressText = fmt.Sprintf("queued #%d", q)
			}
		}
		d.rows[row] = table.Row{mark, truncate(st.item.Title, 32), statusShort(st.status), progressText}
	}
	d.dirtyRows = make(map[int]struct{})
	d.table.SetRows(d.rows)
}

// ensureStateIndex indexes newly appended states incrementally. States are
// append-only, so rebuilding the whole map on every progress event (O(n) per
// event, O(n²) per run in batch mode) is unnecessary.
func (d *Dashboard) ensureStateIndex() {
	if d.stateIndex == nil {
		d.stateIndex = make(map[string]int)
	}
	for ; d.indexedStates < len(d.states); d.indexedStates++ {
		st := &d.states[d.indexedStates]
		if !st.isHeader {
			d.stateIndex[st.item.Key()] = d.indexedStates
		}
	}
}

func (d *Dashboard) markDirty(i int) {
	if row, ok := d.rowOfState(i); ok {
		if d.dirtyRows == nil {
			d.dirtyRows = make(map[int]struct{})
		}
		d.dirtyRows[row] = struct{}{}
	}
	for i > 0 && !d.states[i-1].isHeader {
		i--
	}
	if d.states[i].isHeader {
		if row, ok := d.rowOfState(i); ok {
			d.dirtyRows[row] = struct{}{}
		}
	}
}

func (d *Dashboard) rebuildVisible() {
	oldState := -1
	if len(d.visible) > 0 && d.table.Cursor() >= 0 && d.table.Cursor() < len(d.visible) {
		oldState = d.visible[d.table.Cursor()]
	}
	if d.folded == nil {
		d.folded = make(map[int]bool)
	}
	if d.manuallyUnfolded == nil {
		d.manuallyUnfolded = make(map[int]bool)
	}
	match := func(st itemState) bool {
		switch d.filter {
		case filterFailed:
			return st.status == model.StatusFailed
		case filterActive:
			return st.status == model.StatusDownloading
		case filterPending:
			return st.status == model.StatusPending
		default:
			return true
		}
	}
	var visible []int
	for i := 0; i < len(d.states); i++ {
		st := d.states[i]
		if !st.isHeader {
			if match(st) {
				visible = append(visible, i)
			}
			continue
		}
		end := i + 1
		for end < len(d.states) && !d.states[end].isHeader {
			end++
		}
		children := make([]int, 0, end-i-1)
		for j := i + 1; j < end; j++ {
			if match(d.states[j]) {
				children = append(children, j)
			}
		}
		if len(children) > 0 {
			visible = append(visible, i)
			if !d.folded[i] {
				visible = append(visible, children...)
			}
		}
		i = end - 1
	}
	// Single-item runs have no album header and must remain visible.
	if len(d.states) > 0 && len(visible) == 0 && d.filter == filterAll {
		for i := range d.states {
			visible = append(visible, i)
		}
	}
	d.visible = visible
	d.visibleReady = true
	d.rows = make([]table.Row, len(visible))
	d.dirtyRows = make(map[int]struct{}, len(visible))
	for i := range visible {
		d.dirtyRows[i] = struct{}{}
	}
	d.refreshRows()
	if len(visible) > 0 {
		row := 0
		if oldState >= 0 {
			if r, ok := d.rowOfState(oldState); ok {
				row = r
			} else {
				for h := oldState; h >= 0; h-- {
					if d.states[h].isHeader {
						if r, ok := d.rowOfState(h); ok {
							row = r
						}
						break
					}
				}
			}
		}
		if row >= len(visible) {
			row = len(visible) - 1
		}
		d.selectRow(row)
	} else {
		d.table.SetCursor(0)
	}
}

func (d *Dashboard) skipHeader(row int) bool {
	idx := d.stateAt(row)
	return d.states[idx].isHeader && !d.folded[idx]
}

func (d *Dashboard) selectRow(row int) {
	d.table.SetCursor(0)
	d.table.MoveUp(0)
	d.table.MoveDown(row)
}

func (d *Dashboard) stateAt(row int) int {
	if d.visibleReady && len(d.visible) == 0 {
		return -1
	}
	if !d.visibleReady {
		if row < 0 {
			return 0
		}
		if row >= len(d.states) {
			return len(d.states) - 1
		}
		return row
	}
	if row < 0 {
		row = 0
	}
	if row >= len(d.visible) {
		row = len(d.visible) - 1
	}
	return d.visible[row]
}

func (d *Dashboard) rowOfState(stateIdx int) (int, bool) {
	for row, idx := range d.visible {
		if idx == stateIdx {
			return row, true
		}
	}
	return 0, false
}

func (d *Dashboard) pendingQueueNumber(stateIdx int) int {
	queue := 0
	for i := range d.states {
		if d.states[i].isHeader || d.states[i].status != model.StatusPending {
			continue
		}
		queue++
		if i == stateIdx {
			return queue
		}
	}
	return 0
}

func renderBar(done, total int64, width int) string {
	if width <= 0 {
		return ""
	}
	fraction := 0.0
	if total > 0 {
		fraction = float64(done) / float64(total)
	}
	if fraction > 1 {
		fraction = 1
	}
	if fraction < 0 {
		fraction = 0
	}
	filled := int(fraction * float64(width))
	return strings.Repeat("▓", filled) + strings.Repeat("░", width-filled)
}

func (d *Dashboard) runProgressSums() (done, total int64, unknown int) {
	for _, st := range d.states {
		if st.isHeader {
			continue
		}
		size := st.total
		if size <= 0 {
			size = st.item.Size
		}
		if size > 0 {
			total += size
			if st.status == model.StatusCompleted {
				done += size
			} else {
				done += st.bytes
			}
		} else if st.status != model.StatusCompleted {
			unknown++
		}
	}
	return
}

func (d *Dashboard) setStatus(format string, args ...any) {
	d.statusSeq++
	d.status = fmt.Sprintf(format, args...)
	d.autoClearIn = d.statusSeq
}
func (d *Dashboard) statusClearCmd() tea.Cmd {
	seq := d.statusSeq
	return tea.Tick(3*time.Second, func(time.Time) tea.Msg { return statusClearMsg{seq: seq} })
}

func (d *Dashboard) settled() bool {
	for _, st := range d.states {
		if !st.isHeader && (st.status == model.StatusDownloading || st.status == model.StatusPending) {
			return false
		}
	}
	return true
}

func (d *Dashboard) autoFoldCompletedAlbums() {
	if d.folded == nil {
		d.folded = make(map[int]bool)
	}
	if d.manuallyUnfolded == nil {
		d.manuallyUnfolded = make(map[int]bool)
	}
	changed := false
	for i, st := range d.states {
		if !st.isHeader || d.manuallyUnfolded[i] || d.folded[i] {
			continue
		}
		end := i + 1
		failed, any, complete := false, false, true
		for end < len(d.states) && !d.states[end].isHeader {
			child := d.states[end]
			any = true
			if child.status == model.StatusFailed {
				failed = true
			}
			if child.status == model.StatusDownloading || child.status == model.StatusPending {
				complete = false
			}
			end++
		}
		if any && complete && !failed {
			d.folded[i] = true
			changed = true
		}
	}
	if changed {
		d.rebuildVisible()
	}
}

func (d *Dashboard) toggleFoldAtCursor() {
	if len(d.states) == 0 {
		return
	}
	idx := d.stateAt(d.table.Cursor())
	if idx < 0 || idx >= len(d.states) {
		return
	}
	header := idx
	for header >= 0 && !d.states[header].isHeader {
		header--
	}
	if header < 0 {
		return
	}
	if d.folded[header] {
		d.folded[header] = false
		d.manuallyUnfolded[header] = true
	} else {
		d.folded[header] = true
	}
	d.rebuildVisible()
	d.refreshRows()
	d.fitTableHeight()
}

// --- View ---

// tableChrome returns the terminal rows the batch view spends on
// everything around the table in the current state: header, stats, panel
// borders, footer, and any open status/error/search sections. The table
// gets whatever height is left.
func (d *Dashboard) tableChrome() int {
	chrome := 9 // leading blank, header, bar, stats, blank, panel border(2), trailing blank, footer
	if d.status != "" {
		chrome += 2
	}
	if d.inspectMsg != "" {
		chrome += 2
	}
	if d.showErrors && len(d.errorLog) > 0 {
		chrome += 1 + lipgloss.Height(d.viewErrorPanel())
	}
	if d.searching || d.searchQuery != "" {
		chrome += 2
	}
	return chrome
}

// fitTableHeight gives the table the terminal's leftover height. The
// dashboard previously capped the table at 15 rows, so a fullscreen
// terminal still scrolled; a WindowSizeMsg and every scrape refresh
// re-fit instead.
func (d *Dashboard) fitTableHeight() {
	if !d.ready || d.height <= 0 {
		return
	}
	h := d.height - d.tableChrome()
	if h < 4 {
		h = 4
	}
	if rows := len(d.rows); rows > 0 && h > rows {
		h = rows // no dead space below a short list
	}
	d.table.SetHeight(h)
	d.selectRow(d.table.Cursor())
}

func (d *Dashboard) View() string {
	if d.quitting {
		return ""
	}

	logo := d.theme.Cyan.Bold(true).Render(logo)
	site := d.theme.Dim.Render(fmt.Sprintf(" %s → %s", d.site, d.dest))
	header := clipLine(lipgloss.JoinHorizontal(lipgloss.Left, logo, site), d.width)

	if d.phase == phaseLoading {
		return d.viewLoading(header)
	}
	if d.phase == phaseDone && d.loadingErr != "" && len(d.states) == 0 {
		return d.viewError(header)
	}
	if d.phase == phaseSummary {
		return d.viewSummary(header)
	}
	return d.viewBatch(header)
}

func (d *Dashboard) viewLoading(header string) string {
	msg := d.spinner.View() + " " + d.theme.Dim.Render("fetching media items...")
	if d.batchMode && d.batchIdx < len(d.batchURLs) {
		album := d.batchURLs[d.batchIdx]
		msg = d.spinner.View() + " " + d.theme.Dim.Render(fmt.Sprintf("scraping %s (%s)...", album.Slug, album.Site))
	}
	return lipgloss.JoinVertical(lipgloss.Left,
		"", header, "",
		d.theme.PanelSlim.Render(clipLine(msg, d.width-4)), "",
		d.theme.Footer.Render("  q quit"),
	)
}

func (d *Dashboard) viewError(header string) string {
	return lipgloss.JoinVertical(lipgloss.Left,
		"", header, "",
		d.theme.Error.Render(clipLine("  ✗ "+d.loadingErr, d.width)), "",
		d.theme.Footer.Render("  q quit"),
	)
}

func (d *Dashboard) viewBatch(header string) string {
	tablePanel := d.theme.PanelSlim.Render(d.table.View())
	stats := d.renderStats()
	footer := d.theme.Footer.Render(clipLine("  ↑↓/jk select • c/C cancel • r/R retry • F failed • / search • f filter • Tab fold • e errors • q quit", d.width))

	done, total, unknown := d.runProgressSums()
	label := ""
	if total > 0 {
		pct := int(100 * float64(done) / float64(total))
		if pct > 100 {
			pct = 100
		}
		mark := ""
		if unknown > 0 {
			mark = "~"
		}
		label = fmt.Sprintf(" %s/%s %d%%%s", HumanizeBytes(done), HumanizeBytes(total), pct, mark)
	} else if unknown > 0 {
		label = " unknown size"
	}
	bar := renderBar(done, total, max(0, d.width-lipgloss.Width(label))) + label
	sections := []string{"", header, bar, stats, "", tablePanel}
	if d.status != "" {
		sections = append(sections, "", d.theme.Dim.Render(clipLine("  "+d.status, d.width)))
	}
	if d.inspectMsg != "" {
		sections = append(sections, "", d.theme.Error.Render(clipLine("  "+d.inspectMsg, d.width)))
	}
	if d.showErrors && len(d.errorLog) > 0 {
		sections = append(sections, "", d.viewErrorPanel())
	}
	if d.searching {
		sections = append(sections, "", d.theme.Dim.Render(clipLine("  search: "+d.searchQuery+"▏", d.width)))
	} else if d.searchQuery != "" {
		sections = append(sections, "", d.theme.Dim.Render(clipLine("  search: "+d.searchQuery+"  (n next)", d.width)))
	}
	sections = append(sections, "", footer)
	return lipgloss.JoinVertical(lipgloss.Left, sections...)
}

// appendError records a scrape error for the in-TUI error panel (`e`),
// keeping the log bounded so a long batch cannot grow it without limit.
func (d *Dashboard) appendError(line string) {
	const maxErrors = 200
	d.errorLog = append(d.errorLog, line)
	if len(d.errorLog) > maxErrors {
		d.errorLog = d.errorLog[len(d.errorLog)-maxErrors:]
	}
}

// viewErrorPanel renders the most recent scrape errors for the `e` overlay.
func (d *Dashboard) viewErrorPanel() string {
	shown := 8
	if d.ready {
		available := d.height - 9 - 4 - 2
		for _, open := range []bool{d.status != "", d.inspectMsg != "", d.searching || d.searchQuery != ""} {
			if open {
				available -= 2
			}
		}
		shown = max(0, min(shown, available))
	}
	lines := d.errorLog
	if len(lines) > shown {
		lines = lines[len(lines)-shown:]
	}
	var b strings.Builder
	fmt.Fprintf(&b, "  Errors (%d)", len(d.errorLog))
	for _, line := range lines {
		b.WriteByte('\n')
		b.WriteString(clipLine("  "+line, d.width))
	}
	return d.theme.Error.Render(b.String())
}

func (d *Dashboard) renderStats() string {
	var downloading, completed, failed int
	var totalSpeed float64

	for _, st := range d.states {
		if st.isHeader {
			continue
		}
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

	total := 0
	for _, st := range d.states {
		if !st.isHeader {
			total++
		}
	}
	parts := []string{
		fmt.Sprintf("%s/%s", d.theme.Value.Render(fmt.Sprintf("%d", completed)), d.theme.Dim.Render(fmt.Sprintf("%d", total))),
		d.theme.Dim.Render("active ") + d.theme.Cyan.Render(fmt.Sprintf("%d", downloading)),
		d.theme.Dim.Render("failed ") + d.theme.Error.Render(fmt.Sprintf("%d", failed)),
	}
	if d.filter != filterAll {
		names := []string{"all", "failed", "active", "pending"}
		parts = append(parts, d.theme.Dim.Render("filter ")+d.theme.Cyan.Render(names[d.filter]))
	}

	if totalSpeed > 0 {
		parts = append(parts, d.theme.Dim.Render("speed ")+d.theme.Cyan.Render(fmt.Sprintf("%s/s", HumanizeBytes(int64(totalSpeed)))))
		// A confident ETA that ignores the unknown-size backlog is
		// worse than none: hide it while any outstanding item's size
		// is unknown.
		if eta, unknown := d.remainingETA(totalSpeed); eta > 0 && !unknown {
			parts = append(parts, d.theme.Dim.Render("eta ")+d.theme.Cyan.Render(eta.Round(time.Second).String()))
		}
	}

	if d.batchMode && d.batchRemaining > 0 {
		albumTotal := len(d.batchURLs)
		parts = append(parts, d.theme.Dim.Render(fmt.Sprintf("scraping %d/%d", albumTotal-d.batchRemaining, albumTotal)))
	} else if d.batchMode && len(d.batchURLs) > 1 {
		parts = append(parts, d.theme.Dim.Render(fmt.Sprintf("%d albums", len(d.batchURLs))))
	}

	icon := d.spinner.View()
	if !d.spinnerActive() {
		icon = d.theme.Success.Render("✓")
		if failed > 0 {
			icon = d.theme.Error.Render("✗")
		}
	}

	line := icon + " " + d.theme.Dim.Render("done ") + strings.Join(parts, "  ")
	return d.theme.StatusBar.Render(clipLine(line, d.width-4))
}

func (d *Dashboard) viewSummary(header string) string {
	var completed, failed, cancelled int
	for _, st := range d.states {
		if st.isHeader {
			continue
		}
		switch st.status {
		case model.StatusCompleted:
			completed++
		case model.StatusFailed:
			failed++
		case model.StatusCancelled:
			cancelled++
		}
	}
	body := fmt.Sprintf("  %d completed, %d failed, %d cancelled", completed, failed, cancelled)
	if failed == 0 {
		body += "  ✓ nothing to redo"
	}
	sections := []string{"", header, "", d.theme.PanelSlim.Render(clipLine(body, d.width-4))}
	if d.status != "" {
		sections = append(sections, "", d.theme.Dim.Render(clipLine("  "+d.status, d.width)))
	}
	sections = append(sections, "", d.theme.Footer.Render(clipLine("  R retry failed  •  L write retry list  •  q quit", d.width)))
	return lipgloss.JoinVertical(lipgloss.Left, sections...)
}

// remainingETA estimates how long the outstanding bytes will take at the
// current combined speed, and whether any outstanding item has an
// unknown size. When unknown is true the estimate ignores that backlog
// entirely, so the caller hides the ETA rather than show a confident
// number that is too low.
func (d *Dashboard) remainingETA(totalSpeed float64) (eta time.Duration, unknown bool) {
	if totalSpeed <= 0 {
		return 0, false
	}
	var remaining int64
	for _, st := range d.states {
		if st.isHeader {
			continue
		}
		switch st.status {
		case model.StatusDownloading:
			if st.total > 0 {
				if st.total > st.bytes {
					remaining += st.total - st.bytes
				}
			} else {
				unknown = true
			}
		case model.StatusPending:
			if st.item.Size > 0 {
				remaining += st.item.Size
			} else {
				unknown = true
			}
		}
	}
	if remaining <= 0 {
		return 0, unknown
	}
	return time.Duration(float64(remaining)/totalSpeed) * time.Second, unknown
}

// spinnerActive reports whether the spinner should keep ticking: while
// media is loading, or while any batch item is still downloading. Once
// everything's done, ticking stops instead of animating forever.
func (d *Dashboard) spinnerActive() bool {
	if d.phase == phaseLoading {
		return true
	}
	if d.batchRemaining > 0 {
		return true
	}
	for _, st := range d.states {
		if st.status == model.StatusPending || st.status == model.StatusDownloading {
			return true
		}
	}
	return false
}

// --- Helpers ---

func clipLine(text string, width int) string {
	text = strings.Join(strings.Fields(text), " ")
	if width <= 0 {
		return text
	}
	return lipgloss.NewStyle().MaxWidth(width).Render(text)
}

func HumanizeBytes(n int64) string {
	return fileutil.HumanBytes(n)
}

func statusShort(s model.DownloadStatus) string {
	return model.DownloadStatusString(s)
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
