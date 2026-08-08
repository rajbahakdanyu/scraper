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

	states     []itemState
	stateIndex map[string]int
	rows       []table.Row
	dirtyRows  map[int]struct{}
	downloader Downloader
	dest       string

	width  int
	height int
	ready  bool

	quitting bool
	status   string
	site     string
	phase    phase

	scraperFn  func(context.Context) ([]model.MediaItem, error)
	loadingErr string

	albumSlug        string
	progressMu       sync.Mutex
	pending          map[string]model.ProgressEvent
	refreshTickActive bool

	batchMode      bool
	batchURLs      []DashAlbumURL
	batchIdx       int
	batchRemaining int
	scrapeCtx      context.Context
	cancelScrape   context.CancelFunc

	inspectMsg string
}

// Futuristic logo — clean, minimal
const logo = "◢◤  SCRAPER  ◢◤"

func New(scraperFn func(context.Context) ([]model.MediaItem, error), dl Downloader, dest string, site string) *Dashboard {
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
	d.scrapeCtx, d.cancelScrape = context.WithCancel(context.Background())
	return d
}

// SetAlbumSlug stores the URL slug for creating album subfolders.
func (d *Dashboard) SetAlbumSlug(slug string) {
	d.albumSlug = slug
}

// NewBatch creates a dashboard for batch mode. Items are appended
// incrementally as each album is scraped via the scrape-chain in Init.
func NewBatch(dl Downloader, destRoot string, urls []DashAlbumURL) *Dashboard {
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

	case batchScrapeMsg:
		return d.handleBatchScrape(msg)

	case tea.KeyMsg:
		switch msg.String() {
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
		case "enter":
			d.inspectSelected()
			return d, nil
		case "r":
			d.inspectMsg = ""
			return d, d.retrySelected()
		}

	case progressMsg:
		d.applyProgress(model.ProgressEvent(msg))
		d.refreshRows()
		return d, nil

	case refreshTickMsg:
		d.applyPendingProgress()
		d.refreshRows()
		if d.spinnerActive() || d.hasPending() {
			return d, tea.Tick(time.Second/15, func(t time.Time) tea.Msg { return refreshTickMsg{} })
		}
		d.refreshTickActive = false
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
	if msg.err != nil {
		d.status = "warning: " + msg.err.Error()
	}

	d.states = make([]itemState, len(msg.items))
	for i := range msg.items {
		model.AssignIDs(msg.items[i:i+1], fmt.Sprintf("%d:", i))
		it := msg.items[i]
		d.states[i] = itemState{item: it, status: model.StatusPending, total: it.Size}
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
		d.batchRemaining--
		d.batchIdx++
		if d.batchRemaining == 0 && len(d.states) == 0 {
			d.phase = phaseDone
			return d, nil
		}
		return d, d.startNextScrape()
	}

	d.batchRemaining--
	d.batchIdx++

	if len(msg.items) == 0 {
		if msg.err != nil {
			d.status = "warning: " + msg.err.Error()
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
			item:  it,
			total: it.Size,
			dest:  msg.album.Dest,
		}
		if it.URL == "" {
			st.status = model.StatusCompleted
		} else {
			st.status = model.StatusPending
		}
		d.states = append(d.states, st)
	}

	if wasFirstAlbum {
		h := len(d.states)
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
	totalRows := len(d.states)
	h := totalRows
	if h > 15 {
		h = 15
	}
	d.table.SetHeight(h)
	d.refreshRows()

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
	} else if msg.err != nil {
		d.status = "warning: " + msg.err.Error()
	}
	return d, tea.Batch(cmds...)
}

func (d *Dashboard) moveUpSkippingHeaders() {
	d.table.MoveUp(1)
	idx := d.table.Cursor()
	// Clamp to the first non-header row, scanning upward.
	for idx > 0 && idx < len(d.states) && d.states[idx].isHeader {
		d.table.MoveUp(1)
		idx = d.table.Cursor()
	}
}

func (d *Dashboard) moveDownSkippingHeaders() {
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
	idx := d.table.Cursor()
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
	d.refreshRows()
}

func (d *Dashboard) inspectSelected() {
	d.inspectMsg = ""
	idx := d.table.Cursor()
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

// retrySelected resets the selected failed item back to pending and
// kicks off a fresh download attempt.
func (d *Dashboard) retrySelected() tea.Cmd {
	if d.downloader == nil {
		return nil
	}
	idx := d.table.Cursor()
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
	if evt.Status == model.StatusFailed || evt.Status == model.StatusCancelled {
		return
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
	return
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
	}
	d.markDirty(i)
}

// --- Rendering ---

func (d *Dashboard) refreshRows() {
	if len(d.rows) != len(d.states) {
		d.rows = make([]table.Row, len(d.states))
		d.dirtyRows = make(map[int]struct{}, len(d.states))
		for i := range d.states {
			d.dirtyRows[i] = struct{}{}
		}
	}
	for i := range d.dirtyRows {
		st := d.states[i]
		if st.isHeader {
			var done, active int
			for j := i + 1; j < len(d.states); j++ {
				if d.states[j].isHeader {
					break
				}
				switch d.states[j].status {
				case model.StatusCompleted:
					done++
				case model.StatusDownloading:
					active++
				}
			}
			total := 0
			for j := i + 1; j < len(d.states); j++ {
				if d.states[j].isHeader {
					break
				}
				total++
			}
			headerText := fmt.Sprintf("▾ %s  (%s)  —  %d items", st.albumName, st.albumSite, total)
			if total > 0 {
				if done == total {
					headerText += fmt.Sprintf(", %d done", done)
				} else {
					headerText += fmt.Sprintf(", %d done, %d active", done, active)
				}
			}
			styled := d.theme.Cyan.Bold(true).Render(headerText)
			d.rows[i] = table.Row{"", styled, "", ""}
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

		d.rows[i] = table.Row{mark, truncate(st.item.Title, 40), statusShort(st.status), miniBar(st)}
	}
	d.dirtyRows = make(map[int]struct{})
	d.table.SetRows(d.rows)
}

func (d *Dashboard) ensureStateIndex() {
	if len(d.stateIndex) == len(d.states) {
		return
	}
	d.stateIndex = make(map[string]int, len(d.states))
	for i := range d.states {
		if !d.states[i].isHeader {
			d.stateIndex[d.states[i].item.Key()] = i
		}
	}
}

func (d *Dashboard) markDirty(i int) {
	if d.dirtyRows == nil {
		d.dirtyRows = make(map[int]struct{})
	}
	d.dirtyRows[i] = struct{}{}
	for i > 0 && !d.states[i-1].isHeader {
		i--
	}
	if d.states[i].isHeader {
		d.dirtyRows[i] = struct{}{}
	}
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
	if d.batchMode && d.batchIdx < len(d.batchURLs) {
		album := d.batchURLs[d.batchIdx]
		msg = d.spinner.View() + " " + d.theme.Dim.Render(fmt.Sprintf("scraping %s (%s)...", album.Slug, album.Site))
	}
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
	footer := d.theme.Footer.Render("  ↑↓ select  •  c cancel  •  r retry  •  enter inspect  •  q quit")

	sections := []string{"", header, "", stats, "", tablePanel}
	if d.status != "" {
		sections = append(sections, "", d.theme.Dim.Render("  "+d.status))
	}
	if d.inspectMsg != "" {
		sections = append(sections, "", d.theme.Error.Render("  "+d.inspectMsg))
	}
	sections = append(sections, "", footer)
	return lipgloss.JoinVertical(lipgloss.Left, sections...)
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

	if totalSpeed > 0 {
		parts = append(parts, d.theme.Dim.Render("speed ")+d.theme.Cyan.Render(fmt.Sprintf("%s/s", HumanizeBytes(int64(totalSpeed)))))
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
	return d.theme.StatusBar.Render(line)
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
