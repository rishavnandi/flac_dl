//go:build !vercel && !server

package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func (m model) View() string {
	if m.width == 0 {
		return ""
	}
	if m.width < 40 || m.height < 10 {
		return fg(colMuted).Render("flac_dl: terminal too small")
	}
	contentW := m.width - 4
	bodyH := m.bodyHeight()

	lines := make([]string, 0, m.height-2)
	lines = append(lines, m.headerView(contentW))
	lines = append(lines, m.searchView(contentW))
	lines = append(lines, m.bodyView(contentW, bodyH)...)
	lines = append(lines, m.footerView(contentW))
	return frameStyle.Render(strings.Join(lines, "\n"))
}

func (m model) headerView(w int) string {
	brand := lipgloss.NewStyle().Foreground(colAccent).Background(colCanvas).Render("◆") +
		" " + lipgloss.NewStyle().Foreground(colText).Bold(true).Background(colCanvas).Render("flac_dl")
	sep := fg(colBorder).Render("  /  ")

	crumb := "results"
	if m.mode == modeHome {
		kind := "Songs"
		if m.chartTab == "albums" {
			kind = "Albums"
		}
		crumb = "US Top " + kind
	} else if m.mode == modeAlbum {
		crumb = "album"
		if m.albumTitle != "" {
			crumb += " · " + m.albumTitle
		}
	}
	left := brand + sep + fg(colMuted).Render(truncate(crumb, max(8, w-40)))

	right := fg(colMuted).Render("kuwo")
	switch {
	case m.loading || m.mode == modeHome && m.chartPending > 0:
		right = m.spinner.View() + " " + fg(colMuted).Render("working")
	case m.mode == modeHome:
		right = fg(colMuted).Render(fmt.Sprintf("%d · Apple", len(m.chartItems())))
	case len(m.tracks) > 0 && m.mode == modeAlbum:
		right = fg(colMuted).Render(fmt.Sprintf("%d tracks", len(m.tracks)))
	case len(m.tracks) > 0:
		right = fg(colMuted).Render(fmt.Sprintf("%d of %d", len(m.tracks), m.total))
	}

	gap := w - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	return fillTo(left+bgSpaces(gap, colCanvas)+right, w, colCanvas)
}

func (m model) searchView(w int) string {
	hint := "/ search"
	if m.focusSearch {
		hint = "enter search · esc close"
	}
	left := m.input.View()
	right := fg(colDim).Render(hint)
	gap := w - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	return fillTo(left+bgSpaces(gap, colCanvas)+right, w, colCanvas)
}

func (m model) bodyView(w, h int) []string {
	if m.mode == modeHelp {
		return centerLines(m.helpLines(w), w, h)
	}
	dw := 0
	if w >= 96 {
		dw = detailWidth
	}
	listW := w
	if dw > 0 {
		listW = w - dw - 2
	}
	if m.mode == modeHome {
		list := m.chartListView(listW, h)
		if dw == 0 {
			return list
		}
		detail := m.chartDetailView(dw, h)
		divider := fg(colBorder).Render("│")
		out := make([]string, h)
		for i := 0; i < h; i++ {
			out[i] = list[i] + bgSpaces(1, colCanvas) + divider + detail[i]
		}
		return out
	}
	list := m.listView(listW, h)
	if dw == 0 {
		return list
	}
	detail := m.detailView(dw, h)
	divider := fg(colBorder).Render("│")
	out := make([]string, h)
	for i := 0; i < h; i++ {
		out[i] = list[i] + bgSpaces(1, colCanvas) + divider + detail[i]
	}
	return out
}

func (m model) chartListView(w, h int) []string {
	items := m.chartItems()
	if len(items) == 0 {
		return centerLines(m.chartEmptyLines(w), w, h)
	}
	out := make([]string, h)
	for i := range out {
		idx := m.offset + i
		if idx < len(items) {
			out[i] = m.chartRow(items[idx], idx, w)
		} else {
			out[i] = bgSpaces(w, colCanvas)
		}
	}
	return out
}

func (m model) chartRow(item appleChartItem, i, w int) string {
	selected := i == m.cursor
	bg := colCanvas
	if selected {
		bg = colRaised
	}
	paint := func(c lipgloss.Color) lipgloss.Style {
		return lipgloss.NewStyle().Foreground(c).Background(bg)
	}
	bar := " "
	if selected {
		bar = paint(colAccent).Render("▌")
	}
	index := paint(colDim).Render(fmt.Sprintf("%3d", i+1))
	artistW := 0
	if w >= 64 {
		artistW = 20
	}
	fixed := 7
	if artistW > 0 {
		fixed++
	}
	titleW := w - fixed - artistW
	if titleW < 8 {
		artistW = 0
		titleW = w - 7
	}
	titleColor := colBody
	if selected {
		titleColor = colText
	}
	row := bar + " " + index + "  " + paint(titleColor).Render(padRight(item.Name, titleW))
	if artistW > 0 {
		row += " " + paint(colMuted).Render(padRight(item.Artist, artistW))
	}
	return fillTo(row, w, bg)
}

func (m model) chartEmptyLines(w int) []string {
	if m.chartPending > 0 {
		return []string{m.spinner.View() + " " + fg(colMuted).Render("loading US top "+m.chartTab)}
	}
	if err := m.chartErrors[m.chartTab]; err != "" {
		return []string{
			fg(colAccent).Render("Apple charts unavailable"),
			fg(colDim).Render(truncate(err, max(10, w-8))),
			fg(colDim).Render("press r to retry"),
		}
	}
	return []string{fg(colDim).Render("no chart entries")}
}

func (m model) chartDetailView(w, h int) []string {
	items := m.chartItems()
	if m.cursor < 0 || m.cursor >= len(items) {
		return centerLines([]string{fg(colDim).Render("select a chart entry")}, w, h)
	}
	item := items[m.cursor]
	const padX = 2
	inner := w - padX*2
	lines := make([]string, 0, h)
	if art := m.art[m.artKey]; art != "" {
		for _, line := range strings.Split(art, "\n") {
			lines = append(lines, bgSpaces(padX, colCanvas)+line+bgSpaces(padX, colCanvas))
		}
	} else {
		message := "no cover"
		if m.artPending[m.artKey] {
			message = "loading cover…"
		}
		for _, line := range centerLines([]string{fg(colDim).Render(message)}, inner, inner/2) {
			lines = append(lines, bgSpaces(padX, colCanvas)+line+bgSpaces(padX, colCanvas))
		}
	}
	wrap := lipgloss.NewStyle().Width(inner).Background(colCanvas)
	add := func(s string) {
		for _, line := range strings.Split(s, "\n") {
			lines = append(lines, bgSpaces(padX, colCanvas)+line+bgSpaces(padX, colCanvas))
		}
	}
	lines = append(lines, bgSpaces(w, colCanvas))
	title := strings.Split(wrap.Foreground(colText).Bold(true).Render(item.Name), "\n")
	if len(title) > 2 {
		title = title[:2]
	}
	add(strings.Join(title, "\n"))
	add(wrap.Foreground(colMuted).Render(item.Artist))
	lines = append(lines, bgSpaces(w, colCanvas))
	source := "Apple Music · US top " + m.chartTab
	if item.ReleaseDate != "" {
		source += " · " + item.ReleaseDate
	}
	add(wrap.Foreground(colDim).Render(source))
	if updated := m.chartData[m.chartTab].Updated; updated != "" {
		add(wrap.Foreground(colDim).Render("updated " + updated))
	}
	lines = append(lines, bgSpaces(w, colCanvas))
	add(keyHint("enter", "find on Kuwo"))
	for len(lines) < h {
		lines = append(lines, bgSpaces(w, colCanvas))
	}
	if len(lines) > h {
		lines = lines[:h]
	}
	return lines
}

func (m model) listView(w, h int) []string {
	if len(m.tracks) == 0 || m.errText != "" {
		return centerLines(m.emptyLines(w), w, h)
	}
	out := make([]string, h)
	for i := 0; i < h; i++ {
		idx := m.offset + i
		if idx < len(m.tracks) {
			out[i] = m.trackRow(m.tracks[idx], idx, w)
		} else {
			out[i] = bgSpaces(w, colCanvas)
		}
	}
	return out
}

func (m model) trackRow(t track, i, w int) string {
	selected := i == m.cursor
	bg := colCanvas
	if selected {
		bg = colRaised
	}
	paint := func(c lipgloss.Color) lipgloss.Style {
		return lipgloss.NewStyle().Foreground(c).Background(bg)
	}

	bar := " "
	if selected {
		bar = paint(colAccent).Render("▌")
	}
	index := paint(colDim).Render(fmt.Sprintf("%3d", i+1))

	artistW := 0
	if w >= 64 {
		artistW = 14
	}
	fixed := 19
	if artistW > 0 {
		fixed = 20
	}
	titleW := w - fixed - artistW
	if titleW < 6 {
		artistW = 0
		titleW = w - fixed - artistW
	}
	if titleW < 1 {
		return fillTo(bar+" "+index, w, bg)
	}

	titleColor := colBody
	if selected {
		titleColor = colText
	}
	title := paint(titleColor).Render(padRight(t.Name, titleW))

	row := bar + " " + index + "  " + title
	if artistW > 0 {
		row += " " + paint(colMuted).Render(padRight(t.Artist, artistW))
	}
	best, _ := pickQuality(t, "auto")
	quality := strings.ToUpper(best.Format)
	qualityColor := colMuted
	if best.Format == "flac" {
		qualityColor = colAccent
	}
	row += " " + paint(colDim).Render(padLeft(clock(t.Duration), 5))
	row += " " + paint(qualityColor).Render(padLeft(quality, 5))
	return fillTo(row, w, bg)
}

func (m model) emptyLines(w int) []string {
	switch {
	case m.loading:
		return []string{m.spinner.View() + " " + fg(colMuted).Render("searching")}
	case m.errText != "":
		return []string{
			fg(colAccent).Render("search failed"),
			fg(colDim).Render(truncate(m.errText, max(10, w-8))),
		}
	case m.query == "":
		hint := "/ to focus, then type and press enter"
		if m.focusSearch {
			hint = "type a query above, then press enter"
		}
		return []string{
			lipgloss.NewStyle().Foreground(colText).Bold(true).Background(colCanvas).Render("Search for music"),
			fg(colDim).Render(hint),
		}
	default:
		return []string{
			lipgloss.NewStyle().Foreground(colText).Bold(true).Background(colCanvas).Render("No results"),
			fg(colDim).Render("nothing matched \"" + truncate(m.query, max(10, w-24)) + "\""),
		}
	}
}

func (m model) detailView(w, h int) []string {
	t := m.current()
	if t == nil {
		return centerLines([]string{fg(colDim).Render("select a track to see details")}, w, h)
	}

	const padX = 2
	inner := w - padX*2
	lines := make([]string, 0, h)

	// Cover art.
	if art := m.art[m.artKey]; art != "" {
		for _, l := range strings.Split(art, "\n") {
			lines = append(lines, bgSpaces(padX, colCanvas)+l+bgSpaces(padX, colCanvas))
		}
	} else {
		msg := "no cover"
		if m.artPending[m.artKey] {
			msg = "loading cover…"
		}
		for _, l := range centerLines([]string{fg(colDim).Render(msg)}, inner, inner/2) {
			lines = append(lines, bgSpaces(padX, colCanvas)+l+bgSpaces(padX, colCanvas))
		}
	}

	wrap := lipgloss.NewStyle().Width(inner).Background(colCanvas)
	add := func(s string) {
		for _, l := range strings.Split(s, "\n") {
			lines = append(lines, bgSpaces(padX, colCanvas)+l+bgSpaces(padX, colCanvas))
		}
	}

	lines = append(lines, bgSpaces(w, colCanvas))
	titleLines := strings.Split(wrap.Foreground(colText).Bold(true).Render(t.Name), "\n")
	if len(titleLines) > 2 {
		titleLines = titleLines[:2]
	}
	add(strings.Join(titleLines, "\n"))
	add(wrap.Foreground(colMuted).Render(t.Artist))
	lines = append(lines, bgSpaces(w, colCanvas))
	add(wrap.Foreground(colDim).Render(t.AlbumName))

	best, err := pickQuality(*t, m.quality)
	if err != nil {
		add(fg(colAccent).Render(err.Error()))
	} else {
		lines = append(lines, bgSpaces(w, colCanvas))
		label := strings.ToUpper(best.Format)
		if best.Format == "mp3" {
			label = "MP3 " + string(best.Bitrate) + "k"
		}
		add(fg(colBody).Render(label) + fg(colDim).Render(" · "+best.Size))
		add(fg(colDim).Render(clock(t.Duration) + " · " + qualityName(m.quality)))
	}

	lines = append(lines, bgSpaces(w, colCanvas))
	action := keyHint("d", "download")
	if m.mode == modeAlbum {
		action += keyHint("A", "album")
	}
	add(action)

	for len(lines) < h {
		lines = append(lines, bgSpaces(w, colCanvas))
	}
	if len(lines) > h {
		lines = lines[:h]
	}
	return lines
}

func (m model) footerView(w int) string {
	if m.dl.active {
		label := m.dl.label
		if m.dl.tracks > 1 {
			label = fmt.Sprintf("[%d/%d done] %s", m.dl.finished, m.dl.tracks, label)
		}
		pct := 0
		if m.dl.total > 0 {
			pct = int(m.dl.done * 100 / m.dl.total)
		}
		left := fg(colAccent).Render("↓ ") + fg(colBody).Render(truncate(label, max(8, w-52)))
		right := fg(colMuted).Render(fmt.Sprintf("%3d%%  %s / %s", pct, megabytes(m.dl.done), megabytes(m.dl.total)))
		return fillTo(left+"  "+progressBar(m.dl.done, m.dl.total, 16)+"  "+right, w, colCanvas)
	}
	if m.toastText != "" {
		return fillTo(fg(colMuted).Render(truncate(m.toastText, w)), w, colCanvas)
	}
	if m.playbackStatus != "" {
		icon, action := "▶", "pause"
		switch m.playbackStatus {
		case "loading":
			icon, action = "◌", "cancel"
		case "paused":
			icon, action = "Ⅱ", "resume"
		case "pausing":
			icon, action = "…", "pausing"
		case "resuming":
			icon, action = "…", "resuming"
		}
		left := fg(colAccent).Render(icon+" ") +
			fg(colBody).Render(truncate(m.playbackLabel, max(8, w-34)))
		return fillTo(left+"  "+keyHint("space", action)+keyHint("s", "stop"), w, colCanvas)
	}
	if m.mode == modeHome {
		return fillTo(
			keyHint("1", "songs")+keyHint("2", "albums")+keyHint("↑↓", "move")+
				keyHint("enter", "find on Kuwo")+keyHint("/", "search")+keyHint("r", "refresh"),
			w, colCanvas,
		)
	}
	if w < 90 {
		return fillTo(
			fg(colDim).Render("/ search · ↑↓ move · enter album · space play · d download · ? help"),
			w, colCanvas,
		)
	}
	hints := keyHint("/", "search") +
		keyHint("↑↓", "move") +
		keyHint("enter", "album") +
		keyHint("space", "play") +
		keyHint("s", "stop") +
		keyHint("d", "download") +
		keyHint("tab", qualityName(m.quality)) +
		keyHint("?", "keys")
	return fillTo(hints, w, colCanvas)
}

func keyHint(k, desc string) string {
	key := lipgloss.NewStyle().Foreground(colBody).Background(colRaised).Padding(0, 1).Render(k)
	return key + " " + fg(colDim).Render(desc) + "  "
}

func (m model) helpLines(w int) []string {
	type row struct{ key, desc string }
	rows := []row{
		{"1 / 2", "switch between US top songs and top albums"},
		{"r", "refresh Apple charts"},
		{"/", "focus the search box"},
		{"enter", "find a chart item on Kuwo, or open an album"},
		{"↑ ↓  j k", "move through the list"},
		{"pgup pgdn", "jump half a page"},
		{"esc", "back to results, or leave the search box"},
		{"d", "download the selected track"},
		{"A", "download every track of the open album"},
		{"space", "stream/play the selected Kuwo result, toggle pause/resume"},
		{"s", "stop playback"},
		{"tab", "quality: auto, FLAC, 320k, 128k"},
		{"l", "download lyrics beside the audio"},
		{"?", "toggle this help"},
		{"q", "quit"},
	}
	keyW := 10
	if w < 60 {
		keyW = 9
	}
	lines := make([]string, 0, len(rows)+4)
	lines = append(lines, lipgloss.NewStyle().Foreground(colText).Bold(true).Background(colCanvas).Render("keys"))
	lines = append(lines, "")
	for _, r := range rows {
		lines = append(lines, fg(colBody).Render(padRight(r.key, keyW))+fg(colDim).Render(r.desc))
	}
	lines = append(lines, "")
	status := "quality " + qualityName(m.quality) + " · lyrics " + onOff(m.lrc) + " · saving to " + downloadDir + "/"
	lines = append(lines, fg(colMuted).Render(truncate(status, max(20, w-10))))
	lines = append(lines, fg(colDim).Render("any key to close"))

	box := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colBorder).
		BorderBackground(colCanvas).
		Background(colCanvas).
		Padding(1, 2).
		Render(strings.Join(lines, "\n"))
	return strings.Split(box, "\n")
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}
