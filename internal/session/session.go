// Package session is the frontend-neutral download-run state machine shared
// by the TUI dashboard and the GUI. A Session is not goroutine-safe: one
// owner (the Bubble Tea update loop, or the GUI's controller goroutine)
// mutates it. Blocking work is described as Tasks that the owner runs with
// Exec on any goroutine and feeds back through Apply.
package session

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rajbahakdanyu/scraper/internal/fileutil"
	"github.com/rajbahakdanyu/scraper/internal/model"
)

type Phase int

const (
	PhaseLoading Phase = iota
	PhaseDownload
	PhaseDone
	PhaseSummary
)

type Filter int

const (
	FilterAll Filter = iota
	FilterFailed
	FilterActive
	FilterPending
	filterCount
)

func (f Filter) String() string {
	return [...]string{"all", "failed", "active", "pending"}[f]
}

// Matches reports whether an item with status passes the filter.
func (f Filter) Matches(status model.DownloadStatus) bool {
	switch f {
	case FilterFailed:
		return status == model.StatusFailed
	case FilterActive:
		return status == model.StatusDownloading
	case FilterPending:
		return status == model.StatusPending
	default:
		return true
	}
}

// maxErrors bounds the error log so a long batch cannot grow it forever.
const maxErrors = 200

// Item is one row of the run: a media item or, with Header set, the album
// header that groups the items after it.
type Item struct {
	Item     model.MediaItem
	Status   model.DownloadStatus
	Bytes    int64
	Total    int64
	Speed    float64
	Started  time.Time
	Dest     string
	Header   bool
	Album    string
	Site     string
	AlbumURL string
	Err      error
	// Path is where a completed item was saved.
	Path          string
	progressAt    time.Time
	progressBytes int64
}

// Session is the state of one download run. Exported fields are read by
// the frontends; only the owner may write them.
type Session struct {
	Items      []Item
	Downloader model.Downloader
	Dest       string
	Site       string
	Phase      Phase
	LoadingErr string

	AlbumSlug string
	SourceURL string
	ErrorLog  []string
	// Status is the latest transient message; StatusSeq increments on
	// every change so a frontend can expire it.
	Status    string
	StatusSeq int

	BatchMode      bool
	Albums         []model.Album
	BatchIdx       int
	BatchRemaining int
	ScrapeErrs     []error

	Folded           map[int]bool
	ManuallyUnfolded map[int]bool
	Filter           Filter

	stateIndex    map[string]int
	indexedStates int
	drainStarted  bool
	progressMu    sync.Mutex
	pending       map[string]model.ProgressEvent

	scraperFn    func(context.Context) ([]model.MediaItem, error)
	scrapeCtx    context.Context
	cancelScrape context.CancelFunc
}

// New creates a single-URL session; scraperFn produces its items.
func New(scraperFn func(context.Context) ([]model.MediaItem, error), dl model.Downloader, dest, site string) *Session {
	s := newSession(dl, dest, site)
	s.scraperFn = scraperFn
	return s
}

// NewBatch creates a session that scrapes albums in order, appending each
// album's items as it arrives.
func NewBatch(dl model.Downloader, destRoot string, albums []model.Album) *Session {
	site := fmt.Sprintf("%d albums", len(albums))
	if len(albums) == 1 {
		site = albums[0].Site
	}
	s := newSession(dl, destRoot, site)
	s.BatchMode = true
	s.Albums = albums
	s.BatchRemaining = len(albums)
	return s
}

func newSession(dl model.Downloader, dest, site string) *Session {
	s := &Session{
		Downloader:       dl,
		Dest:             dest,
		Site:             site,
		Phase:            PhaseLoading,
		Folded:           map[int]bool{},
		ManuallyUnfolded: map[int]bool{},
	}
	s.scrapeCtx, s.cancelScrape = context.WithCancel(context.Background())
	return s
}

// --- Tasks ---

type TaskKind int

const (
	TaskScrape TaskKind = iota
	TaskDownload
)

// Task is blocking work for the owner to run with Exec.
type Task struct {
	Kind TaskKind
	// Album is the scraped album for batch scrape tasks; single-URL
	// scrapes leave it zero.
	Album model.Album
	Batch bool
	Item  model.MediaItem
	Dest  string
}

// Result is a finished Task, to be passed to Apply.
type Result struct {
	Task  Task
	Items []model.MediaItem
	Path  string
	Err   error
}

// Start returns the run's first task: the (first) scrape.
func (s *Session) Start() []Task {
	if s.BatchMode {
		return s.nextScrape()
	}
	return []Task{{Kind: TaskScrape}}
}

func (s *Session) nextScrape() []Task {
	if s.BatchIdx >= len(s.Albums) {
		return nil
	}
	return []Task{{Kind: TaskScrape, Batch: true, Album: s.Albums[s.BatchIdx]}}
}

// Exec runs t. It only reads state fixed at construction, so it is safe on
// any goroutine.
func (s *Session) Exec(t Task) Result {
	switch t.Kind {
	case TaskDownload:
		path, err := s.Downloader.Download(t.Item, t.Dest)
		return Result{Task: t, Path: path, Err: err}
	default:
		if t.Batch {
			items, err := t.Album.Scraper.Parse(s.scrapeCtx, t.Album.URL)
			return Result{Task: t, Items: items, Err: err}
		}
		items, err := s.scraperFn(s.scrapeCtx)
		return Result{Task: t, Items: items, Err: err}
	}
}

// Apply folds a finished task into the state and returns follow-up tasks.
func (s *Session) Apply(r Result) []Task {
	switch {
	case r.Task.Kind == TaskDownload:
		s.ApplyDone(r.Task.Item.Key(), r.Err)
		if i, ok := s.IndexOf(r.Task.Item.Key()); ok && s.Items[i].Status == model.StatusCompleted {
			s.Items[i].Path = r.Path
		}
		return nil
	case r.Task.Batch:
		return s.applyBatchScrape(r.Task.Album, r.Items, r.Err)
	default:
		return s.applyScrape(r.Items, r.Err)
	}
}

func downloadTask(item model.MediaItem, dest string) Task {
	return Task{Kind: TaskDownload, Item: item, Dest: dest}
}

// --- Scrape results ---

func (s *Session) applyScrape(items []model.MediaItem, err error) []Task {
	if err != nil && len(items) == 0 {
		s.LoadingErr = err.Error()
		s.AppendError(err.Error())
		s.Phase = PhaseDone
		return nil
	}
	if len(items) == 0 {
		s.LoadingErr = "no media items found"
		s.Phase = PhaseDone
		return nil
	}
	if err != nil {
		s.SetStatus("warning: %v", err)
		s.AppendError(err.Error())
	}

	s.Items = make([]Item, len(items))
	for i := range items {
		model.AssignIDs(items[i:i+1], fmt.Sprintf("%d:", i))
		it := items[i]
		st := Item{Item: it, Status: model.StatusPending, Total: it.Size, AlbumURL: s.SourceURL}
		if it.URL == "" {
			st.Status = model.StatusCompleted
		}
		s.Items[i] = st
	}

	// Album (multiple files): download into a subfolder named by the slug.
	if len(items) > 1 && s.AlbumSlug != "" {
		albumDest, err := fileutil.SafeJoin(s.Dest, s.AlbumSlug)
		if err == nil {
			err = fileutil.EnsureDir(albumDest)
		}
		if err != nil {
			s.Items = nil
			s.LoadingErr = err.Error()
			s.Phase = PhaseDone
			return nil
		}
		s.Dest = albumDest
	}

	s.Phase = PhaseDownload
	s.startDrain()
	return s.StartAllDownloads()
}

func (s *Session) applyBatchScrape(album model.Album, items []model.MediaItem, err error) []Task {
	if err != nil && len(items) == 0 {
		s.LoadingErr = fmt.Sprintf("%s: %v", album.URL, err)
		s.ScrapeErrs = append(s.ScrapeErrs, errors.New(s.LoadingErr))
		s.AppendError(s.LoadingErr)
		s.BatchRemaining--
		s.BatchIdx++
		if s.BatchRemaining == 0 && len(s.Items) == 0 {
			s.Phase = PhaseDone
			return nil
		}
		return s.nextScrape()
	}

	if err != nil {
		s.SetStatus("warning: %v", err)
		s.AppendError(fmt.Sprintf("%s: %v", album.URL, err))
	}

	s.BatchRemaining--
	s.BatchIdx++

	if len(items) == 0 {
		if s.BatchRemaining > 0 {
			return s.nextScrape()
		}
		if len(s.Items) == 0 {
			s.Phase = PhaseDone
		}
		return nil
	}

	firstAlbum := s.Phase == PhaseLoading

	if err := fileutil.EnsureDir(album.Dest); err != nil {
		albumErr := fmt.Errorf("%s: %w", album.URL, err)
		s.ScrapeErrs = append(s.ScrapeErrs, albumErr)
		s.AppendError(albumErr.Error())
		if s.BatchRemaining == 0 && len(s.Items) == 0 {
			s.LoadingErr = err.Error()
			s.Phase = PhaseDone
			return nil
		}
		return s.nextScrape()
	}

	base := len(s.Items)
	s.Items = append(s.Items, Item{Header: true, Album: album.Slug, Site: album.Site})
	for i := range items {
		model.AssignIDs(items[i:i+1], fmt.Sprintf("%d:%d:", s.BatchIdx-1, i))
		it := items[i]
		st := Item{Item: it, Total: it.Size, Dest: album.Dest, AlbumURL: album.URL, Status: model.StatusPending}
		if it.URL == "" {
			st.Status = model.StatusCompleted
		}
		s.Items = append(s.Items, st)
	}

	var tasks []Task
	if firstAlbum {
		s.Phase = PhaseDownload
		s.startDrain()
		tasks = s.StartAllDownloads()
	} else if s.Downloader != nil {
		for i := base + 1; i < len(s.Items); i++ {
			if s.Items[i].Item.URL == "" {
				continue
			}
			s.Downloader.Prepare(s.Items[i].Item)
			tasks = append(tasks, downloadTask(s.Items[i].Item, s.Items[i].Dest))
		}
	}
	if s.BatchRemaining > 0 {
		tasks = append(tasks, s.nextScrape()...)
	}
	return tasks
}

func (s *Session) StartAllDownloads() []Task {
	if s.Downloader == nil {
		return nil
	}
	var tasks []Task
	for i := range s.Items {
		st := s.Items[i]
		if st.Header || st.Item.URL == "" {
			continue
		}
		s.Downloader.Prepare(st.Item)
		tasks = append(tasks, downloadTask(st.Item, s.destOf(i)))
	}
	return tasks
}

func (s *Session) destOf(i int) string {
	if s.Items[i].Dest != "" {
		return s.Items[i].Dest
	}
	return s.Dest
}

// --- Progress ---

// startDrain consumes the downloader's progress channel in the background
// until Shutdown closes it, keeping only each item's latest event.
func (s *Session) startDrain() {
	if s.Downloader == nil || s.drainStarted {
		return
	}
	s.drainStarted = true
	ch := s.Downloader.Progress()
	go func() {
		for evt := range ch {
			s.progressMu.Lock()
			if s.pending == nil {
				s.pending = make(map[string]model.ProgressEvent)
			}
			s.pending[evt.Item.Key()] = evt
			s.progressMu.Unlock()
		}
	}()
}

// HasPending reports buffered progress not yet applied. Goroutine-safe.
func (s *Session) HasPending() bool {
	s.progressMu.Lock()
	defer s.progressMu.Unlock()
	return len(s.pending) > 0
}

// ApplyPending applies the buffered progress events.
func (s *Session) ApplyPending() {
	s.progressMu.Lock()
	pending := s.pending
	s.pending = make(map[string]model.ProgressEvent)
	s.progressMu.Unlock()
	for _, evt := range pending {
		s.ApplyProgress(evt)
	}
}

func (s *Session) ensureStateIndex() {
	if s.stateIndex == nil {
		s.stateIndex = make(map[string]int)
	}
	for ; s.indexedStates < len(s.Items); s.indexedStates++ {
		st := &s.Items[s.indexedStates]
		if !st.Header {
			s.stateIndex[st.Item.Key()] = s.indexedStates
		}
	}
}

// IndexOf returns the Items index of the item with key.
func (s *Session) IndexOf(key string) (int, bool) {
	s.ensureStateIndex()
	i, ok := s.stateIndex[key]
	return i, ok
}

// ApplyProgress folds one progress event into its item.
func (s *Session) ApplyProgress(evt model.ProgressEvent) {
	i, ok := s.IndexOf(evt.Item.Key())
	if !ok {
		return
	}
	// Terminal states are authoritative: a progress event that was
	// buffered before the item finished (cancelled, completed, failed)
	// must not resurrect it back to an active state.
	switch s.Items[i].Status {
	case model.StatusCompleted, model.StatusFailed, model.StatusCancelled:
		return
	}
	// Terminal status values (Failed, Cancelled) must only come from
	// ApplyDone, not from buffered progress events — a stale
	// StatusFailed event queued before a retry would otherwise
	// corrupt the retried item.
	switch evt.Status {
	case model.StatusFailed, model.StatusCancelled:
		return
	case model.StatusCompleted:
		evt.Status = model.StatusDownloading
	}
	st := &s.Items[i]
	now := time.Now()
	if st.Started.IsZero() {
		st.Started = now
	}
	if !st.progressAt.IsZero() && evt.Bytes >= st.progressBytes {
		if elapsed := now.Sub(st.progressAt).Seconds(); elapsed > 0 {
			st.Speed = float64(evt.Bytes-st.progressBytes) / elapsed
		}
	}
	st.progressAt = now
	st.progressBytes = evt.Bytes
	st.Status = evt.Status
	st.Bytes = evt.Bytes
	if evt.Total > 0 {
		st.Total = evt.Total
	}
}

// ApplyDone records a download's authoritative outcome.
func (s *Session) ApplyDone(key string, err error) {
	i, ok := s.IndexOf(key)
	if !ok {
		return
	}
	st := &s.Items[i]
	switch {
	case err == nil:
		st.Status = model.StatusCompleted
		st.Err = nil
		if st.Total > 0 {
			st.Bytes = st.Total
		}
	case st.Status == model.StatusCancelled:
		// A cancelled download also returns an error here, but that's not
		// a failure worth downgrading the status for.
	default:
		st.Status = model.StatusFailed
		st.Err = err
		s.SetStatus("✗ %s: %v", model.TitleOf(st.Item), err)
	}
}

// Tick recomputes download speeds from elapsed time, so speed and ETA
// keep moving between progress events.
func (s *Session) Tick() {
	for i := range s.Items {
		st := &s.Items[i]
		if st.Status == model.StatusDownloading && !st.progressAt.IsZero() && time.Since(st.progressAt) >= 3*time.Second {
			st.Speed = 0
		}
	}
}

// Refresh applies buffered progress, auto-folds finished albums and moves
// a settled run to the summary. It reports whether refreshing should
// continue.
func (s *Session) Refresh() bool {
	s.ApplyPending()
	s.AutoFold()
	if s.Phase == PhaseDownload && s.BatchRemaining == 0 && len(s.Items) > 0 && s.Settled() {
		s.Phase = PhaseSummary
		return false
	}
	return s.Active() || s.HasPending()
}

// --- Status and errors ---

func (s *Session) SetStatus(format string, args ...any) {
	s.StatusSeq++
	s.Status = fmt.Sprintf(format, args...)
}

// AppendError records a scrape error for the error log.
func (s *Session) AppendError(line string) {
	s.ErrorLog = append(s.ErrorLog, line)
	if len(s.ErrorLog) > maxErrors {
		s.ErrorLog = s.ErrorLog[len(s.ErrorLog)-maxErrors:]
	}
}

// --- Actions ---

// Cancel cancels item i if it is still pending or downloading.
func (s *Session) Cancel(i int) bool {
	if s.Downloader == nil || i < 0 || i >= len(s.Items) {
		return false
	}
	st := &s.Items[i]
	if st.Header || (st.Status != model.StatusPending && st.Status != model.StatusDownloading) {
		return false
	}
	s.Downloader.Cancel(st.Item)
	st.Status = model.StatusCancelled
	return true
}

// CancelAll cancels every pending or downloading item.
func (s *Session) CancelAll() bool {
	changed := false
	for i := range s.Items {
		if s.Cancel(i) {
			changed = true
		}
	}
	return changed
}

// Retry resets failed item i and returns its new download task.
func (s *Session) Retry(i int) []Task {
	if s.Downloader == nil || i < 0 || i >= len(s.Items) {
		return nil
	}
	if s.Items[i].Header || s.Items[i].Status != model.StatusFailed {
		return nil
	}
	tasks := []Task{s.reset(i)}
	s.resume()
	return tasks
}

// RetryAllFailed resets every failed item and returns their download tasks.
func (s *Session) RetryAllFailed() []Task {
	if s.Downloader == nil {
		return nil
	}
	var tasks []Task
	for i := range s.Items {
		if s.Items[i].Header || s.Items[i].Status != model.StatusFailed {
			continue
		}
		tasks = append(tasks, s.reset(i))
	}
	if len(tasks) > 0 {
		s.resume()
	}
	return tasks
}

func (s *Session) reset(i int) Task {
	st := &s.Items[i]
	st.Status = model.StatusPending
	st.Bytes = 0
	st.Total = st.Item.Size
	st.Speed = 0
	st.Started = time.Time{}
	st.progressAt = time.Time{}
	st.progressBytes = 0
	st.Err = nil
	st.Path = ""
	s.Downloader.Prepare(st.Item)
	return downloadTask(st.Item, s.destOf(i))
}

// resume re-arms summary detection when a retry restarts a settled run.
func (s *Session) resume() {
	if s.Phase == PhaseSummary || s.Phase == PhaseDone {
		s.Phase = PhaseDownload
	}
}

// DismissSummary leaves the summary for the item list.
func (s *Session) DismissSummary() {
	if s.Phase == PhaseSummary {
		s.Phase = PhaseDone
	}
}

// Shutdown aborts the scrape and every download; closing the progress
// channel also ends the drain goroutine.
func (s *Session) Shutdown() {
	if s.cancelScrape != nil {
		s.cancelScrape()
	}
	if s.Downloader != nil {
		s.Downloader.Shutdown()
	}
}

// CycleFilter advances to the next list filter.
func (s *Session) CycleFilter() {
	s.Filter = (s.Filter + 1) % filterCount
}

// HeaderOf returns the album header index at or above i, or -1.
func (s *Session) HeaderOf(i int) int {
	if i >= len(s.Items) {
		return -1
	}
	for ; i >= 0; i-- {
		if s.Items[i].Header {
			return i
		}
	}
	return -1
}

// ToggleFold folds or unfolds the album containing item i. Unfolding by
// hand stops the album from auto-folding again.
func (s *Session) ToggleFold(i int) bool {
	h := s.HeaderOf(i)
	if h < 0 {
		return false
	}
	if s.Folded[h] {
		s.Folded[h] = false
		s.ManuallyUnfolded[h] = true
	} else {
		s.Folded[h] = true
	}
	return true
}

// AutoFold folds albums that finished without failures.
func (s *Session) AutoFold() bool {
	changed := false
	for i, st := range s.Items {
		if !st.Header || s.ManuallyUnfolded[i] || s.Folded[i] {
			continue
		}
		failed, any, complete := false, false, true
		for j := i + 1; j < len(s.Items) && !s.Items[j].Header; j++ {
			any = true
			switch s.Items[j].Status {
			case model.StatusFailed:
				failed = true
			case model.StatusDownloading, model.StatusPending:
				complete = false
			}
		}
		if any && complete && !failed {
			s.Folded[i] = true
			changed = true
		}
	}
	return changed
}

// Visible returns the Items indices shown under the current filter and
// folds: headers with at least one matching child, then their children
// unless folded.
func (s *Session) Visible() []int {
	var visible []int
	for i := 0; i < len(s.Items); i++ {
		if !s.Items[i].Header {
			if s.Filter.Matches(s.Items[i].Status) {
				visible = append(visible, i)
			}
			continue
		}
		end := i + 1
		for end < len(s.Items) && !s.Items[end].Header {
			end++
		}
		var children []int
		for j := i + 1; j < end; j++ {
			if s.Filter.Matches(s.Items[j].Status) {
				children = append(children, j)
			}
		}
		if len(children) > 0 {
			visible = append(visible, i)
			if !s.Folded[i] {
				visible = append(visible, children...)
			}
		}
		i = end - 1
	}
	// Single-item runs have no album header and must remain visible.
	if len(s.Items) > 0 && len(visible) == 0 && s.Filter == FilterAll {
		for i := range s.Items {
			visible = append(visible, i)
		}
	}
	return visible
}

// FindNext returns the first row after start (wrapping) in visible whose
// non-header item satisfies match, or -1.
func (s *Session) FindNext(visible []int, start int, match func(Item) bool) int {
	for step := 1; step <= len(visible); step++ {
		row := ((start+step)%len(visible) + len(visible)) % len(visible)
		st := s.Items[visible[row]]
		if !st.Header && match(st) {
			return row
		}
	}
	return -1
}

// MatchTitle matches items whose title contains query, case-insensitively.
func MatchTitle(query string) func(Item) bool {
	q := strings.ToLower(query)
	return func(st Item) bool {
		return q != "" && strings.Contains(strings.ToLower(model.TitleOf(st.Item)), q)
	}
}

// IsFailed matches failed items.
func IsFailed(st Item) bool { return st.Status == model.StatusFailed }

// --- Rows ---

// Row is a render-ready view of one visible row.
type Row struct {
	Index int
	State Item
	// Header aggregates.
	Folded              bool
	Done, Failed, Count int
	// QueuePos is the 1-based position of a pending item in the queue.
	QueuePos int
}

// Rows renders visible[from:to]. Queue positions count pending items in
// Items order up to each row.
func (s *Session) Rows(visible []int, from, to int) []Row {
	from, to = max(from, 0), min(to, len(visible))
	if from >= to {
		return nil
	}
	rows := make([]Row, 0, to-from)
	pending, next := 0, 0
	for _, i := range visible[from:to] {
		for ; next <= i; next++ {
			if !s.Items[next].Header && s.Items[next].Status == model.StatusPending {
				pending++
			}
		}
		r := Row{Index: i, State: s.Items[i]}
		if r.State.Header {
			r.Folded = s.Folded[i]
			r.Done, r.Failed, r.Count = s.AlbumCounts(i)
		} else if r.State.Status == model.StatusPending {
			r.QueuePos = pending
		}
		rows = append(rows, r)
	}
	return rows
}

// AlbumCounts returns completed, failed and total children of header h.
func (s *Session) AlbumCounts(h int) (done, failed, total int) {
	for j := h + 1; j < len(s.Items) && !s.Items[j].Header; j++ {
		total++
		switch s.Items[j].Status {
		case model.StatusCompleted:
			done++
		case model.StatusFailed:
			failed++
		}
	}
	return
}

// --- Aggregates ---

// Stats summarizes the run.
type Stats struct {
	Total, Completed, Failed, Cancelled, Active, Pending int
	// Speed is the combined speed of active downloads in bytes/sec.
	Speed float64
	// DoneBytes and TotalBytes cover items of known size; Unknown counts
	// unfinished items without one.
	DoneBytes, TotalBytes int64
	Unknown               int
	// ETA is zero when it cannot be estimated honestly.
	ETA time.Duration
}

func (s *Session) Stats() Stats {
	var st Stats
	for _, it := range s.Items {
		if it.Header {
			continue
		}
		st.Total++
		switch it.Status {
		case model.StatusDownloading:
			st.Active++
			st.Speed += it.Speed
		case model.StatusCompleted:
			st.Completed++
		case model.StatusFailed:
			st.Failed++
		case model.StatusCancelled:
			st.Cancelled++
		case model.StatusPending:
			st.Pending++
		}
	}
	st.DoneBytes, st.TotalBytes, st.Unknown = s.ProgressSums()
	// A confident ETA that ignores the unknown-size backlog is worse than
	// none.
	if eta, unknown := s.RemainingETA(st.Speed); !unknown {
		st.ETA = eta
	}
	return st
}

// ProgressSums returns downloaded and total bytes over items of known
// size, plus the count of unfinished items whose size is unknown.
func (s *Session) ProgressSums() (done, total int64, unknown int) {
	for _, st := range s.Items {
		if st.Header {
			continue
		}
		size := st.Total
		if size <= 0 {
			size = st.Item.Size
		}
		if size > 0 {
			total += size
			if st.Status == model.StatusCompleted {
				done += size
			} else {
				done += st.Bytes
			}
		} else if st.Status != model.StatusCompleted {
			unknown++
		}
	}
	return
}

// RemainingETA estimates how long the outstanding bytes take at
// totalSpeed, and whether any outstanding item has an unknown size (the
// estimate then ignores that backlog).
func (s *Session) RemainingETA(totalSpeed float64) (eta time.Duration, unknown bool) {
	if totalSpeed <= 0 {
		return 0, false
	}
	var remaining int64
	for _, st := range s.Items {
		if st.Header {
			continue
		}
		switch st.Status {
		case model.StatusDownloading:
			if st.Total > 0 {
				if st.Total > st.Bytes {
					remaining += st.Total - st.Bytes
				}
			} else {
				unknown = true
			}
		case model.StatusPending:
			if st.Item.Size > 0 {
				remaining += st.Item.Size
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

// Settled reports whether no item is pending or downloading.
func (s *Session) Settled() bool {
	for _, st := range s.Items {
		if !st.Header && (st.Status == model.StatusDownloading || st.Status == model.StatusPending) {
			return false
		}
	}
	return true
}

// Active reports whether the run is still working: loading, scraping more
// albums, or any item pending or downloading.
func (s *Session) Active() bool {
	if s.Phase == PhaseLoading || s.BatchRemaining > 0 {
		return true
	}
	for _, st := range s.Items {
		if st.Status == model.StatusPending || st.Status == model.StatusDownloading {
			return true
		}
	}
	return false
}

// Failures returns the failed items in display order.
func (s *Session) Failures() []model.FailedItem {
	var out []model.FailedItem
	for _, st := range s.Items {
		if st.Header || st.Status != model.StatusFailed {
			continue
		}
		out = append(out, model.FailedItem{Item: st.Item, Err: st.Err, AlbumURL: st.AlbumURL})
	}
	return out
}

// Result reports how the run ended, mirroring the plain/JSON runners: a
// failed single-URL scrape is a plain error, and failed items or batch
// albums are a *model.PartialError.
func (s *Session) Result() error {
	if !s.BatchMode && s.LoadingErr != "" {
		return errors.New(s.LoadingErr)
	}
	errs := append([]error(nil), s.ScrapeErrs...)
	for _, f := range s.Failures() {
		errs = append(errs, f.Err)
	}
	return model.NewPartialError("download", errs)
}
