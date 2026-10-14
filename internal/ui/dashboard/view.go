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
	"github.com/rajbahakdanyu/scraper/internal/session"
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

func (d *Dashboard) statusClearCmd() tea.Cmd {
	seq := d.StatusSeq
	return tea.Tick(3*time.Second, func(time.Time) tea.Msg { return statusClearMsg{seq: seq} })
}

func (d *Dashboard) View() string {
	if d.quitting {
		return ""
	}

	logo := d.theme.Cyan.Bold(true).Render(logo)
	site := d.theme.Dim.Render(fmt.Sprintf(" %s → %s", displayText(d.Site), displayText(d.Dest)))
	header := clipLine(lipgloss.JoinHorizontal(lipgloss.Left, logo, site), d.width)

	if d.Phase == session.PhaseLoading {
		return d.viewLoading(header)
	}
	if d.Phase == session.PhaseDone && d.LoadingErr != "" && len(d.Items) == 0 {
		return d.viewError(header)
	}
	if d.Phase == session.PhaseSummary {
		return d.viewSummary(header)
	}
	return d.viewBatch(header)
}

func (d *Dashboard) viewLoading(header string) string {
	msg := d.spinner.View() + " " + d.theme.Dim.Render("fetching media items...")
	if d.BatchMode && d.BatchIdx < len(d.Albums) {
		album := d.Albums[d.BatchIdx]
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
		d.theme.Error.Render(clipLine("  ✗ "+displayText(d.LoadingErr), d.width)), "",
		d.theme.Footer.Render("  q quit"),
	)
}

func (d *Dashboard) viewBatch(header string) string {
	tablePanel := d.theme.PanelSlim.Render(d.table.View())
	stats := d.renderStats()
	footer := d.theme.Footer.Render(clipLine("  ↑↓/jk select • c/C cancel • r/R retry • F failed • / search • f filter • Tab fold • e errors • q quit", d.width))

	done, total, unknown := d.ProgressSums()
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
	if d.Status != "" {
		sections = append(sections, "", d.theme.Dim.Render(clipLine("  "+displayText(d.Status), d.width)))
	}
	if d.inspectMsg != "" {
		sections = append(sections, "", d.theme.Error.Render(clipLine("  "+d.inspectMsg, d.width)))
	}
	if d.showErrors && len(d.ErrorLog) > 0 {
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

// viewErrorPanel renders the most recent scrape errors for the `e` overlay.
func (d *Dashboard) viewErrorPanel() string {
	shown := 8
	if d.ready {
		available := d.height - 9 - 4 - 2
		for _, open := range []bool{d.Status != "", d.inspectMsg != "", d.searching || d.searchQuery != ""} {
			if open {
				available -= 2
			}
		}
		shown = max(0, min(shown, available))
	}
	lines := d.ErrorLog
	if len(lines) > shown {
		lines = lines[len(lines)-shown:]
	}
	var b strings.Builder
	fmt.Fprintf(&b, "  Errors (%d)", len(d.ErrorLog))
	for _, line := range lines {
		b.WriteByte('\n')
		b.WriteString(clipLine("  "+displayText(line), d.width))
	}
	return d.theme.Error.Render(b.String())
}

func (d *Dashboard) renderStats() string {
	st := d.Stats()
	parts := []string{
		fmt.Sprintf("%s/%s", d.theme.Value.Render(fmt.Sprintf("%d", st.Completed)), d.theme.Dim.Render(fmt.Sprintf("%d", st.Total))),
		d.theme.Dim.Render("active ") + d.theme.Cyan.Render(fmt.Sprintf("%d", st.Active)),
		d.theme.Dim.Render("failed ") + d.theme.Error.Render(fmt.Sprintf("%d", st.Failed)),
	}
	if d.Filter != session.FilterAll {
		parts = append(parts, d.theme.Dim.Render("filter ")+d.theme.Cyan.Render(d.Filter.String()))
	}

	if st.Speed > 0 {
		parts = append(parts, d.theme.Dim.Render("speed ")+d.theme.Cyan.Render(fmt.Sprintf("%s/s", fileutil.HumanBytes(int64(st.Speed)))))
		if st.ETA > 0 {
			parts = append(parts, d.theme.Dim.Render("eta ")+d.theme.Cyan.Render(st.ETA.Round(time.Second).String()))
		}
	}

	if d.BatchMode && d.BatchRemaining > 0 {
		albumTotal := len(d.Albums)
		parts = append(parts, d.theme.Dim.Render(fmt.Sprintf("scraping %d/%d", albumTotal-d.BatchRemaining, albumTotal)))
	} else if d.BatchMode && len(d.Albums) > 1 {
		parts = append(parts, d.theme.Dim.Render(fmt.Sprintf("%d albums", len(d.Albums))))
	}

	icon := d.spinner.View()
	if !d.Active() {
		icon = d.theme.Success.Render("✓")
		if st.Failed > 0 {
			icon = d.theme.Error.Render("✗")
		}
	}

	line := icon + " " + d.theme.Dim.Render("done ") + strings.Join(parts, "  ")
	return d.theme.StatusBar.Render(clipLine(line, d.width-4))
}

func (d *Dashboard) viewSummary(header string) string {
	st := d.Stats()
	body := fmt.Sprintf("  %d completed, %d failed, %d cancelled", st.Completed, st.Failed, st.Cancelled)
	if st.Failed == 0 {
		body += "  ✓ nothing to redo"
	}
	sections := []string{"", header, "", d.theme.PanelSlim.Render(clipLine(body, d.width-4))}
	if d.Status != "" {
		sections = append(sections, "", d.theme.Dim.Render(clipLine("  "+displayText(d.Status), d.width)))
	}
	sections = append(sections, "", d.theme.Footer.Render(clipLine("  enter view list  •  R retry failed  •  L write retry list  •  q quit", d.width)))
	return lipgloss.JoinVertical(lipgloss.Left, sections...)
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

func miniBar(st session.Item) string {
	switch st.Status {
	case model.StatusCompleted:
		return "▰▰▰▰▰▰▰▰▰▰ 100%"
	case model.StatusPending:
		return "▱▱▱▱▱▱▱▱▱▱ 0%"
	case model.StatusFailed:
		return "✗ failed"
	case model.StatusCancelled:
		return "⊘ cancelled"
	}
	if st.Total <= 0 {
		return "▰▰▱▱▱▱▱▱▱▱ ???"
	}
	pct := float64(st.Bytes) / float64(st.Total)
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
