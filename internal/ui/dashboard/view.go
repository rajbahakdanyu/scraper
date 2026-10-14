package dashboard

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/rajbahakdanyu/scraper/internal/fileutil"
	"github.com/rajbahakdanyu/scraper/internal/model"
)

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
	d.status = displayText(fmt.Sprintf(format, args...))
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

func (d *Dashboard) View() string {
	if d.quitting {
		return ""
	}

	logo := d.theme.Cyan.Bold(true).Render(logo)
	site := d.theme.Dim.Render(fmt.Sprintf(" %s → %s", displayText(d.site), displayText(d.dest)))
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
		msg = d.spinner.View() + " " + d.theme.Dim.Render(fmt.Sprintf("scraping %s (%s)...", displayText(album.Slug), displayText(album.Site)))
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
		d.theme.Error.Render(clipLine("  ✗ "+displayText(d.loadingErr), d.width)), "",
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
		label = fmt.Sprintf(" %s/%s %d%%%s", fileutil.HumanBytes(done), fileutil.HumanBytes(total), pct, mark)
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
		sections = append(sections, "", d.theme.Dim.Render(clipLine("  search: "+displayText(d.searchQuery)+"▏", d.width)))
	} else if d.searchQuery != "" {
		sections = append(sections, "", d.theme.Dim.Render(clipLine("  search: "+displayText(d.searchQuery)+"  (n next)", d.width)))
	}
	sections = append(sections, "", footer)
	return lipgloss.JoinVertical(lipgloss.Left, sections...)
}

// appendError records a scrape error for the in-TUI error panel (`e`),
// keeping the log bounded so a long batch cannot grow it without limit.
func (d *Dashboard) appendError(line string) {
	const maxErrors = 200
	d.errorLog = append(d.errorLog, displayText(line))
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
		parts = append(parts, d.theme.Dim.Render("speed ")+d.theme.Cyan.Render(fmt.Sprintf("%s/s", fileutil.HumanBytes(int64(totalSpeed)))))
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
	sections = append(sections, "", d.theme.Footer.Render(clipLine("  enter view list  •  R retry failed  •  L write retry list  •  q quit", d.width)))
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

var lineBreaks = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ")

func clipLine(text string, width int) string {
	text = lineBreaks.Replace(text)
	if width <= 0 {
		return text
	}
	return lipgloss.NewStyle().MaxWidth(width).Render(text)
}

// displayText strips terminal controls from untrusted text before it is styled.
func displayText(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\t' || r == '\n' || r == '\r':
			return ' '
		case unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r):
			return -1
		}
		return r
	}, s)
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
