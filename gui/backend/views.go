package backend

import (
	"time"

	"github.com/rajbahakdanyu/scraper/internal/model"
	"github.com/rajbahakdanyu/scraper/internal/session"
)

// RunState is the run summary pushed to the frontend on every change.
type RunState struct {
	Running       bool      `json:"running"`
	Phase         string    `json:"phase"`
	Group         int       `json:"group"`
	Groups        int       `json:"groups"`
	Site          string    `json:"site"`
	Dest          string    `json:"dest"`
	LoadingErr    string    `json:"loadingErr"`
	Status        string    `json:"status"`
	StatusSeq     int       `json:"statusSeq"`
	Filter        string    `json:"filter"`
	Visible       int       `json:"visible"`
	Stats         StatsView `json:"stats"`
	BatchMode     bool      `json:"batchMode"`
	AlbumsTotal   int       `json:"albumsTotal"`
	AlbumsScraped int       `json:"albumsScraped"`
	ScrapingAlbum string    `json:"scrapingAlbum"`
	Errors        int       `json:"errors"`
	Active        bool      `json:"active"`
	Watching      bool      `json:"watching"`
	NextRun       string    `json:"nextRun"`
	Version       uint64    `json:"version"`
}

// StatsView is session.Stats with JSON names.
type StatsView struct {
	Total      int     `json:"total"`
	Completed  int     `json:"completed"`
	Failed     int     `json:"failed"`
	Cancelled  int     `json:"cancelled"`
	Active     int     `json:"active"`
	Pending    int     `json:"pending"`
	Speed      float64 `json:"speed"`
	DoneBytes  int64   `json:"doneBytes"`
	TotalBytes int64   `json:"totalBytes"`
	Unknown    int     `json:"unknown"`
	ETASeconds float64 `json:"etaSeconds"`
}

// RowView is one visible row of the item list.
type RowView struct {
	Row      int     `json:"row"`
	Index    int     `json:"index"`
	Header   bool    `json:"header"`
	Album    string  `json:"album"`
	Site     string  `json:"site"`
	Title    string  `json:"title"`
	Status   string  `json:"status"`
	Bytes    int64   `json:"bytes"`
	Total    int64   `json:"total"`
	Speed    float64 `json:"speed"`
	QueuePos int     `json:"queuePos"`
	Folded   bool    `json:"folded"`
	Done     int     `json:"done"`
	Failed   int     `json:"failed"`
	Count    int     `json:"count"`
	Error    string  `json:"error"`
	HasPath  bool    `json:"hasPath"`
}

// Match is a search hit: the visible row to scroll to and its item index.
type Match struct {
	Row   int `json:"row"`
	Index int `json:"index"`
}

// GroupResult reports one finished group (YouTube or other sites).
type GroupResult struct {
	Group     int    `json:"group"`
	Error     string `json:"error"`
	RetryFile string `json:"retryFile"`
	Summary   string `json:"summary"`
}

// RunResult reports the end of a whole run.
type RunResult struct {
	Error   string `json:"error"`
	Partial bool   `json:"partial"`
}

var phaseNames = map[session.Phase]string{
	session.PhaseLoading:  "loading",
	session.PhaseDownload: "download",
	session.PhaseSummary:  "summary",
	session.PhaseDone:     "done",
}

var filterNames = map[string]session.Filter{
	"all":     session.FilterAll,
	"failed":  session.FilterFailed,
	"active":  session.FilterActive,
	"pending": session.FilterPending,
}

func statsView(st session.Stats) StatsView {
	return StatsView{
		Total:      st.Total,
		Completed:  st.Completed,
		Failed:     st.Failed,
		Cancelled:  st.Cancelled,
		Active:     st.Active,
		Pending:    st.Pending,
		Speed:      st.Speed,
		DoneBytes:  st.DoneBytes,
		TotalBytes: st.TotalBytes,
		Unknown:    st.Unknown,
		ETASeconds: st.ETA.Round(time.Second).Seconds(),
	}
}

func rowView(n int, r session.Row) RowView {
	st := r.State
	v := RowView{
		Row:      n,
		Index:    r.Index,
		Header:   st.Header,
		Album:    st.Album,
		Site:     st.Site,
		Title:    model.TitleOf(st.Item),
		Status:   model.DownloadStatusString(st.Status),
		Bytes:    st.Bytes,
		Total:    st.Total,
		Speed:    st.Speed,
		QueuePos: r.QueuePos,
		Folded:   r.Folded,
		Done:     r.Done,
		Failed:   r.Failed,
		Count:    r.Count,
		HasPath:  st.Path != "",
	}
	if st.Header {
		v.Title, v.Status = "", ""
	}
	if st.Err != nil {
		v.Error = st.Err.Error()
	}
	return v
}
