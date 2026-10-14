package dashboard

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/rajbahakdanyu/scraper/internal/session"
)

func (d *Dashboard) handleScrapeResult(msg scrapeResultMsg) (tea.Model, tea.Cmd) {
	tasks := d.Apply(session.Result{Task: session.Task{Kind: session.TaskScrape}, Items: msg.items, Err: msg.err})
	return d, d.afterScrape(tasks)
}

func (d *Dashboard) handleBatchScrape(msg batchScrapeMsg) (tea.Model, tea.Cmd) {
	tasks := d.Apply(session.Result{Task: session.Task{Kind: session.TaskScrape, Batch: true, Album: msg.album}, Items: msg.items, Err: msg.err})
	return d, d.afterScrape(tasks)
}

func (d *Dashboard) afterScrape(tasks []session.Task) tea.Cmd {
	if d.Phase != session.PhaseDownload {
		d.refreshRows()
		return d.runTasks(tasks)
	}
	d.rebuildVisible()
	d.refreshRows()
	d.fitTableHeight()
	cmds := []tea.Cmd{d.runTasks(tasks)}
	if d.Downloader != nil {
		cmds = append(cmds, d.ensureRefreshTicker())
	}
	return tea.Batch(cmds...)
}

func (d *Dashboard) ensureRefreshTicker() tea.Cmd {
	if d.refreshTickActive {
		return nil
	}
	d.refreshTickActive = true
	return func() tea.Msg { return refreshTickMsg{} }
}

func (d *Dashboard) applyDone(msg downloadDoneMsg) {
	d.ApplyDone(msg.key, msg.err)
}
