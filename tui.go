//go:build !vercel && !server

package main

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const (
	detailWidth = 40
	artColumns  = detailWidth - 4
)

type viewMode int

const (
	modeHome viewMode = iota
	modeResults
	modeAlbum
	modeHelp
)

type downloadState struct {
	active   bool
	label    string
	done     int64
	total    int64
	index    int
	tracks   int
	finished int
}

type savedState struct {
	tracks []track
	cursor int
	offset int
	total  int
	page   int
	query  string
}

type model struct {
	client *client
	charts *appleChartClient

	width, height int

	input       textinput.Model
	focusSearch bool

	spinner spinner.Model
	loading bool

	query        string
	page         int
	total        int
	tracks       []track
	cursor       int
	offset       int
	loadingMore  bool
	searchSeq    int
	albumSeq     int
	chartSeq     int
	chartPending int
	chartTab     string
	chartData    map[string]appleChartFeed
	chartErrors  map[string]string

	mode         viewMode
	helpReturn   viewMode
	searchReturn viewMode
	albumTitle   string

	saved savedState

	quality string
	lrc     bool

	audio          *audioEngine
	playback       *audioSession
	playbackCancel context.CancelFunc
	playbackSeq    int
	playbackKey    string
	playbackLabel  string
	playbackStatus string

	art               map[string]string
	artPending        map[string]bool
	artPrefetched     map[string]bool
	artPrefetchActive bool
	artRetries        map[string]int
	artKey            string
	artTick           int

	errText string

	toastText string
	toastID   int

	dl  downloadState
	sub chan tea.Msg
}

func newModel() model {
	ti := textinput.New()
	ti.Placeholder = "search artists, albums, tracks"
	ti.Prompt = "⌕ "
	ti.CharLimit = 120
	ti.PromptStyle = lipgloss.NewStyle().Foreground(colAccent).Background(colCanvas)
	ti.TextStyle = lipgloss.NewStyle().Foreground(colText).Background(colCanvas)
	ti.PlaceholderStyle = lipgloss.NewStyle().Foreground(colDim).Background(colCanvas)
	ti.Cursor.Style = lipgloss.NewStyle().Foreground(colAccent).Background(colCanvas)
	ti.Blur()

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(colAccent).Background(colCanvas)

	return model{
		client:        newClient(),
		charts:        newAppleChartClient(),
		audio:         &audioEngine{},
		input:         ti,
		chartSeq:      1,
		chartPending:  2,
		chartTab:      "songs",
		chartData:     map[string]appleChartFeed{},
		chartErrors:   map[string]string{},
		spinner:       sp,
		quality:       "auto",
		art:           map[string]string{},
		artPending:    map[string]bool{},
		artPrefetched: map[string]bool{},
		artRetries:    map[string]int{},
	}
}

func runTUI() error {
	p := tea.NewProgram(newModel(), tea.WithAltScreen())
	final, err := p.Run()
	if m, ok := final.(model); ok {
		m.shutdownPlayback()
	}
	return err
}

func (m model) Init() tea.Cmd {
	return batch(
		textinput.Blink,
		chartCmd(m.charts, "songs", m.chartSeq),
		chartCmd(m.charts, "albums", m.chartSeq),
		m.spinner.Tick,
	)
}

// Messages

type searchMsg struct {
	seq   int
	page  int
	more  bool
	list  []track
	total int
	err   error
}

type chartMsg struct {
	seq  int
	kind string
	feed appleChartFeed
	err  error
}

type albumMsg struct {
	seq   int
	title string
	list  []track
	err   error
}

type artMsg struct {
	key      string
	art      string
	err      error
	prefetch bool
}

type artTickMsg struct {
	key    string
	tick   int
	pic    string
	artist string
	album  string
}

type artRetryMsg struct{ key string }

type progressMsg struct {
	done   int64
	total  int64
	index  int
	tracks int
	label  string
}

type artRequest struct {
	key    string
	pic    string
	artist string
	album  string
}

type queueMsg struct {
	index int
	total int
	label string
}

type itemDoneMsg struct{}

type doneMsg struct {
	path    string
	skipped bool
	err     error
}

type albumDoneMsg struct {
	failed  int
	total   int
	skipped int
	errors  []string
}

type toastMsg struct{ id int }

type playbackReadyMsg struct {
	seq     int
	session *audioSession
	err     error
}

type playbackDoneMsg struct {
	seq     int
	session *audioSession
	err     error
}

type playbackControlMsg struct {
	seq     int
	session *audioSession
	status  string
}

// Commands

func searchCmd(c *client, seq int, query string, page int, more bool) tea.Cmd {
	return func() tea.Msg {
		list, total, err := c.searchPage("kuwo", query, page)
		return searchMsg{seq: seq, page: page, more: more, list: list, total: total, err: err}
	}
}

func chartCmd(c *appleChartClient, kind string, seq int) tea.Cmd {
	return func() tea.Msg {
		feed, err := c.fetch(kind)
		return chartMsg{seq: seq, kind: kind, feed: feed, err: err}
	}
}

func albumCmd(c *client, seq int, id string) tea.Cmd {
	return func() tea.Msg {
		col, err := c.collection("album", "kuwo", id)
		title := "album " + id
		if col.Info != nil && col.Info.Name != "" {
			title = col.Info.Name
		}
		return albumMsg{seq: seq, title: title, list: col.List, err: err}
	}
}

func artCmd(req artRequest, prefetch bool, cols int) tea.Cmd {
	return func() tea.Msg {
		art, err := fetchCover(req.pic, req.artist, req.album, cols)
		return artMsg{key: req.key, art: art, err: err, prefetch: prefetch}
	}
}

func playbackCmd(ctx context.Context, engine *audioEngine, c *client, t track, q quality, key string, seq int, old *audioSession) tea.Cmd {
	return func() tea.Msg {
		if old != nil {
			old.stop()
		}
		session, err := engine.open(ctx, c, t, q, key)
		if err == nil && ctx.Err() != nil {
			session.stop()
			session = nil
			err = ctx.Err()
		}
		return playbackReadyMsg{seq: seq, session: session, err: err}
	}
}

func waitPlaybackCmd(seq int, session *audioSession) tea.Cmd {
	return func() tea.Msg {
		<-session.done
		for session.player.IsPlaying() {
			time.Sleep(20 * time.Millisecond)
		}
		return playbackDoneMsg{seq: seq, session: session, err: session.err}
	}
}

func playbackControlCmd(seq int, session *audioSession, status string) tea.Cmd {
	return func() tea.Msg {
		if status == "paused" {
			session.player.Pause()
		} else {
			session.player.Play()
		}
		return playbackControlMsg{seq: seq, session: session, status: status}
	}
}

func stopPlaybackCmd(session *audioSession) tea.Cmd {
	if session == nil {
		return nil
	}
	return func() tea.Msg {
		session.stop()
		return nil
	}
}

func waitFor(ch chan tea.Msg) tea.Cmd {
	return func() tea.Msg { return <-ch }
}

func batch(cmds ...tea.Cmd) tea.Cmd {
	out := make([]tea.Cmd, 0, len(cmds))
	for _, c := range cmds {
		if c != nil {
			out = append(out, c)
		}
	}
	switch len(out) {
	case 0:
		return nil
	case 1:
		return out[0]
	}
	return tea.Batch(out...)
}

func startDownload(c *client, j job, ch chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		go func() {
			path, skipped, err := newDownloader(c).fetchWith(j, func(done, total int64) {
				select {
				case ch <- progressMsg{done: done, total: total, index: 1, tracks: 1, label: label(j.track)}:
				default:
				}
			})
			ch <- doneMsg{path: path, skipped: skipped, err: err}
		}()
		return nil
	}
}

func startAlbumDownload(c *client, jobs []job, skipped int, ch chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		go func() {
			d := newDownloader(c)
			type item struct {
				index int
				job   job
			}
			queue := make(chan item)
			var wg sync.WaitGroup
			var mu sync.Mutex
			var failures []string

			for worker := 0; worker < min(4, len(jobs)); worker++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for next := range queue {
						ch <- queueMsg{index: next.index, total: len(jobs), label: label(next.job.track)}
						_, _, err := d.fetchWith(next.job, func(done, total int64) {
							select {
							case ch <- progressMsg{
								done: done, total: total, index: next.index,
								tracks: len(jobs), label: label(next.job.track),
							}:
							default:
							}
						})
						if err != nil {
							mu.Lock()
							failures = append(failures, label(next.job.track)+": "+err.Error())
							mu.Unlock()
						}
						ch <- itemDoneMsg{}
					}
				}()
			}
			for i, j := range jobs {
				queue <- item{index: i + 1, job: j}
			}
			close(queue)
			wg.Wait()
			ch <- albumDoneMsg{
				failed: len(failures), total: len(jobs),
				skipped: skipped, errors: failures,
			}
		}()
		return nil
	}
}

// Update

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.input.Width = max(12, m.width-42)
		m.ensureVisible()
		return m, m.artForSelection()

	case tea.KeyMsg:
		return m.handleKey(msg)

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		if !m.loading && m.chartPending == 0 {
			cmd = nil
		}
		return m, cmd

	case chartMsg:
		if msg.seq != m.chartSeq {
			return m, nil
		}
		if m.chartPending > 0 {
			m.chartPending--
		}
		if msg.err != nil {
			m.chartErrors[msg.kind] = msg.err.Error()
		} else {
			m.chartData[msg.kind] = msg.feed
			delete(m.chartErrors, msg.kind)
		}
		if m.chartPending == 0 && len(m.chartData["songs"].Results) == 0 &&
			len(m.chartData["albums"].Results) > 0 && m.chartTab == "songs" {
			m.chartTab = "albums"
		}
		m.ensureVisible()
		return m, m.artForSelection()

	case searchMsg:
		if msg.seq != m.searchSeq {
			return m, nil
		}
		m.loading = false
		if msg.err != nil {
			m.loadingMore = false
			m.errText = msg.err.Error()
			return m, m.fireToast("search failed: " + msg.err.Error())
		}
		m.errText = ""
		if msg.more {
			m.tracks = append(m.tracks, msg.list...)
			m.loadingMore = false
		} else {
			m.tracks = msg.list
			m.cursor = 0
			m.offset = 0
		}
		m.total = msg.total
		m.page = msg.page
		m.ensureVisible()
		return m, m.artForSelection()

	case albumMsg:
		if msg.seq != m.albumSeq {
			return m, nil
		}
		m.loading = false
		if msg.err != nil {
			return m, m.fireToast("album failed: " + msg.err.Error())
		}
		m.mode = modeAlbum
		m.albumTitle = msg.title
		m.tracks = msg.list
		m.cursor = 0
		m.offset = 0
		m.total = len(msg.list)
		m.artKey = ""
		m.ensureVisible()
		return m, m.artForSelection()

	case artTickMsg:
		if msg.key != m.artKey || msg.tick != m.artTick || msg.key == "" {
			return m, nil
		}
		if _, ok := m.art[msg.key]; ok {
			return m, nil
		}
		m.artPending[msg.key] = true
		return m, artCmd(artRequest{
			key: msg.key, pic: msg.pic, artist: msg.artist, album: msg.album,
		}, false, artColumns)

	case artRetryMsg:
		if msg.key != m.artKey {
			return m, nil
		}
		return m, m.artForSelection()

	case artMsg:
		delete(m.artPending, msg.key)
		if msg.prefetch {
			m.artPrefetchActive = false
		}
		if msg.err != nil {
			if msg.key == m.artKey && m.artRetries[msg.key] < 1 {
				m.artRetries[msg.key]++
				return m, tea.Tick(time.Second, func(time.Time) tea.Msg {
					return artRetryMsg{key: msg.key}
				})
			}
			if msg.key == m.artKey {
				m.artPrefetched[msg.key] = true
				return m, batch(
					m.fireToast("cover unavailable: "+msg.err.Error()),
					m.prefetchNextArt(),
				)
			}
			return m, m.prefetchNextArt()
		}
		delete(m.artRetries, msg.key)
		m.art[msg.key] = msg.art
		return m, m.prefetchNextArt()

	case playbackReadyMsg:
		if msg.seq != m.playbackSeq {
			return m, stopPlaybackCmd(msg.session)
		}
		m.playbackCancel = nil
		if msg.err != nil {
			m.playbackStatus = ""
			m.playbackKey = ""
			m.playbackLabel = ""
			return m, m.fireToast("playback failed: " + msg.err.Error())
		}
		m.playback = msg.session
		m.playback.player.Play()
		m.playbackStatus = "playing"
		return m, waitPlaybackCmd(msg.seq, msg.session)

	case playbackDoneMsg:
		if msg.seq != m.playbackSeq || msg.session != m.playback {
			return m, nil
		}
		m.playback = nil
		m.playbackStatus = ""
		m.playbackKey = ""
		m.playbackLabel = ""
		if msg.err != nil {
			return m, batch(m.fireToast("playback failed: "+msg.err.Error()), stopPlaybackCmd(msg.session))
		}
		return m, stopPlaybackCmd(msg.session)

	case playbackControlMsg:
		if msg.seq != m.playbackSeq || msg.session != m.playback {
			return m, nil
		}
		m.playbackStatus = msg.status
		return m, nil

	case progressMsg:
		m.dl.done = msg.done
		m.dl.total = msg.total
		m.dl.index = msg.index
		m.dl.tracks = msg.tracks
		m.dl.label = msg.label
		return m, waitFor(m.sub)

	case queueMsg:
		m.dl.index = msg.index
		m.dl.tracks = msg.total
		m.dl.label = msg.label
		m.dl.done = 0
		m.dl.total = 0
		return m, waitFor(m.sub)

	case itemDoneMsg:
		m.dl.finished++
		return m, waitFor(m.sub)

	case doneMsg:
		m.dl.active = false
		if msg.err != nil {
			return m, m.fireToast("download failed: " + msg.err.Error())
		}
		if msg.skipped {
			return m, m.fireToast("already in " + filepath.Base(filepath.Dir(msg.path)) + "/")
		}
		return m, m.fireToast("saved " + filepath.Base(msg.path))

	case albumDoneMsg:
		m.dl.active = false
		if msg.failed > 0 {
			text := strconv.Itoa(msg.failed) + " of " + strconv.Itoa(msg.total) + " downloads failed"
			if len(msg.errors) > 0 {
				text += ": " + strings.Join(msg.errors, "; ")
			}
			return m, m.fireToast(text)
		}
		if msg.skipped > 0 {
			return m, m.fireToast(strconv.Itoa(msg.total) + " tracks saved · " + strconv.Itoa(msg.skipped) + " skipped")
		}
		return m, m.fireToast(strconv.Itoa(msg.total) + " tracks saved to " + downloadDir + "/")

	case toastMsg:
		if msg.id == m.toastID {
			m.toastText = ""
		}
		return m, nil
	}

	return m, nil
}

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if key == "ctrl+c" {
		return m, tea.Quit
	}
	if m.mode == modeHelp {
		m.mode = m.helpReturn
		return m, nil
	}
	if key == "?" {
		m.helpReturn = m.mode
		m.mode = modeHelp
		return m, nil
	}

	if m.focusSearch {
		switch key {
		case "esc":
			m.focusSearch = false
			m.input.Blur()
			m.input.PromptStyle = lipgloss.NewStyle().Foreground(colDim).Background(colCanvas)
			return m, nil
		case "enter":
			q := strings.TrimSpace(m.input.Value())
			if q == "" {
				return m, nil
			}
			m.focusSearch = false
			m.input.Blur()
			m.input.PromptStyle = lipgloss.NewStyle().Foreground(colDim).Background(colCanvas)
			return m, m.startSearch(q)
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}

	switch key {
	case "q":
		return m, tea.Quit
	case "/":
		m.focusSearch = true
		m.input.Focus()
		m.input.PromptStyle = lipgloss.NewStyle().Foreground(colAccent).Background(colCanvas)
		m.input.CursorEnd()
		return m, textinput.Blink
	case "esc":
		if m.mode == modeAlbum {
			m.closeAlbum()
			return m, m.artForSelection()
		}
		if m.mode == modeResults && m.searchReturn == modeHome {
			m.mode = modeHome
			m.tracks = nil
			m.total = 0
			m.page = 0
			m.errText = ""
			m.albumTitle = ""
			m.cursor = 0
			m.offset = 0
			return m, m.artForSelection()
		}
		m.focusSearch = true
		m.input.Focus()
		m.input.PromptStyle = lipgloss.NewStyle().Foreground(colAccent).Background(colCanvas)
		return m, textinput.Blink
	case "up", "k":
		m.moveCursor(-1)
		return m, m.afterMove()
	case "down", "j":
		m.moveCursor(1)
		return m, m.afterMove()
	case "pgup", "ctrl+u":
		m.moveCursor(-m.pageStep())
		return m, m.afterMove()
	case "pgdown", "ctrl+d":
		m.moveCursor(m.pageStep())
		return m, m.afterMove()
	case "home", "g":
		m.setCursor(0)
		return m, m.afterMove()
	case "end", "G":
		m.setCursor(m.itemCount() - 1)
		return m, m.afterMove()
	case "enter":
		if m.mode == modeHome {
			return m, m.searchSelectedChartItem()
		}
		return m, m.openAlbum()
	case "1":
		if m.mode == modeHome {
			m.setChartTab("songs")
			return m, m.artForSelection()
		}
	case "2":
		if m.mode == modeHome {
			m.setChartTab("albums")
			return m, m.artForSelection()
		}
	case "r":
		if m.mode == modeHome {
			return m, m.loadCharts()
		}
	case "d":
		return m, m.downloadSelected()
	case "A":
		return m, m.downloadAlbum()
	case " ":
		return m, m.togglePlayback()
	case "s":
		return m, m.stopPlayback()
	case "tab":
		m.cycleQuality()
		return m, m.fireToast("quality: " + qualityName(m.quality))
	case "l":
		m.lrc = !m.lrc
		if m.lrc {
			return m, m.fireToast("lyrics on")
		}
		return m, m.fireToast("lyrics off")
	}
	return m, nil
}

func (m *model) startSearch(q string) tea.Cmd {
	if m.mode == modeHome || m.mode == modeAlbum {
		m.searchReturn = modeHome
	}
	m.searchSeq++
	m.query = q
	m.input.SetValue(q)
	m.page = 1
	m.tracks = nil
	m.cursor = 0
	m.offset = 0
	m.total = 0
	m.loading = true
	m.loadingMore = false
	m.errText = ""
	m.albumTitle = ""
	m.artKey = ""
	m.mode = modeResults
	return batch(searchCmd(m.client, m.searchSeq, q, 1, false), m.spinner.Tick)
}

func (m *model) searchSelectedChartItem() tea.Cmd {
	items := m.chartItems()
	if m.cursor < 0 || m.cursor >= len(items) {
		return nil
	}
	item := items[m.cursor]
	return m.startSearch(strings.TrimSpace(item.Name + " " + item.Artist))
}

func (m *model) setChartTab(kind string) {
	if kind != "songs" && kind != "albums" {
		return
	}
	m.chartTab = kind
	m.cursor = 0
	m.offset = 0
	m.artKey = ""
	m.ensureVisible()
}

func (m *model) loadCharts() tea.Cmd {
	m.chartSeq++
	m.chartPending = 2
	m.chartErrors = map[string]string{}
	return batch(
		chartCmd(m.charts, "songs", m.chartSeq),
		chartCmd(m.charts, "albums", m.chartSeq),
		m.spinner.Tick,
	)
}

func (m model) chartItems() []appleChartItem {
	return m.chartData[m.chartTab].Results
}

func (m model) itemCount() int {
	if m.mode == modeHome {
		return len(m.chartItems())
	}
	return len(m.tracks)
}

func (m *model) openAlbum() tea.Cmd {
	t := m.current()
	if t == nil {
		return nil
	}
	id := strings.TrimSpace(string(t.AlbumID))
	if id == "" {
		return m.fireToast("no album for this track")
	}
	m.saved.tracks = m.tracks
	m.saved.cursor = m.cursor
	m.saved.offset = m.offset
	m.saved.total = m.total
	m.saved.page = m.page
	m.saved.query = m.query
	m.albumSeq++
	m.loading = true
	return batch(albumCmd(m.client, m.albumSeq, id), m.spinner.Tick)
}

func (m *model) closeAlbum() {
	m.mode = modeResults
	m.albumTitle = ""
	m.tracks = m.saved.tracks
	m.cursor = m.saved.cursor
	m.offset = m.saved.offset
	m.total = m.saved.total
	m.page = m.saved.page
	m.query = m.saved.query
	m.saved = savedState{}
	m.ensureVisible()
}

func (m *model) downloadSelected() tea.Cmd {
	t := m.current()
	if t == nil {
		return nil
	}
	if m.dl.active {
		return m.fireToast("a download is already running")
	}
	q, err := pickQuality(*t, m.quality)
	if err != nil {
		return m.fireToast(err.Error())
	}
	ch := make(chan tea.Msg, 32)
	m.sub = ch
	m.dl = downloadState{active: true, label: label(*t)}
	j := job{platform: "kuwo", track: *t, quality: q, dir: downloadDir, lrc: m.lrc}
	return batch(startDownload(m.client, j, ch), waitFor(ch))
}

func (m *model) downloadAlbum() tea.Cmd {
	if m.mode != modeAlbum {
		return m.fireToast("open an album first (enter)")
	}
	if m.dl.active {
		return m.fireToast("a download is already running")
	}
	jobs := make([]job, 0, len(m.tracks))
	for _, t := range m.tracks {
		q, err := pickQuality(t, m.quality)
		if err != nil {
			continue
		}
		jobs = append(jobs, job{platform: "kuwo", track: t, quality: q, dir: downloadDir, lrc: m.lrc})
	}
	if len(jobs) == 0 {
		return m.fireToast("no tracks have " + qualityName(m.quality))
	}
	ch := make(chan tea.Msg, 64)
	m.sub = ch
	m.dl = downloadState{active: true, tracks: len(jobs)}
	skipped := len(m.tracks) - len(jobs)
	return batch(startAlbumDownload(m.client, jobs, skipped, ch), waitFor(ch))
}

func (m *model) togglePlayback() tea.Cmd {
	if m.mode == modeHome {
		return m.fireToast("find a Kuwo result before playing")
	}
	t := m.current()
	if t == nil {
		return nil
	}
	key := playbackTrackKey(*t)
	if m.playback != nil && m.playback.key == key {
		switch m.playbackStatus {
		case "playing":
			m.playbackStatus = "pausing"
			return playbackControlCmd(m.playbackSeq, m.playback, "paused")
		case "paused":
			m.playbackStatus = "resuming"
			return playbackControlCmd(m.playbackSeq, m.playback, "playing")
		default:
			return nil
		}
	}
	if m.playbackStatus == "loading" && m.playbackKey == key {
		return m.stopPlayback()
	}
	q, err := pickQuality(*t, m.quality)
	if err != nil {
		return m.fireToast(err.Error())
	}
	if m.playbackCancel != nil {
		m.playbackCancel()
	}
	old := m.playback
	m.playback = nil
	m.playbackSeq++
	seq := m.playbackSeq
	ctx, cancel := context.WithCancel(context.Background())
	m.playbackCancel = cancel
	m.playbackKey = key
	m.playbackLabel = label(*t)
	m.playbackStatus = "loading"
	return playbackCmd(ctx, m.audio, m.client, *t, q, key, seq, old)
}

func (m *model) stopPlayback() tea.Cmd {
	if m.playback == nil && m.playbackCancel == nil {
		return nil
	}
	m.playbackSeq++
	if m.playbackCancel != nil {
		m.playbackCancel()
		m.playbackCancel = nil
	}
	session := m.playback
	if session != nil {
		session.cancel()
	}
	m.playback = nil
	m.playbackKey = ""
	m.playbackLabel = ""
	m.playbackStatus = ""
	return stopPlaybackCmd(session)
}

func (m model) shutdownPlayback() {
	if m.playbackCancel != nil {
		m.playbackCancel()
	}
	if m.playback != nil {
		m.playback.stop()
	}
}

func playbackTrackKey(t track) string {
	if t.ID != "" {
		return "kuwo:" + string(t.ID)
	}
	return label(t)
}

func (m *model) moveCursor(delta int) {
	m.setCursor(m.cursor + delta)
}

func (m *model) setCursor(i int) {
	count := m.itemCount()
	if count == 0 {
		m.cursor = 0
		return
	}
	if i < 0 {
		i = 0
	}
	if i >= count {
		i = count - 1
	}
	m.cursor = i
	m.ensureVisible()
	m.artKey = ""
}

func (m *model) ensureVisible() {
	h := m.bodyHeight()
	if h < 1 {
		return
	}
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+h {
		m.offset = m.cursor - h + 1
	}
	if maxOffset := m.itemCount() - h; m.offset > maxOffset {
		m.offset = maxOffset
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

func (m model) afterMove() tea.Cmd {
	if m.mode != modeResults || m.loading || m.loadingMore || m.total == 0 || len(m.tracks) >= m.total {
		return m.artForSelection()
	}
	if m.cursor < len(m.tracks)-4 {
		return m.artForSelection()
	}
	m.loadingMore = true
	return batch(searchCmd(m.client, m.searchSeq, m.query, m.page+1, true), m.artForSelection())
}

func (m *model) artForSelection() tea.Cmd {
	m.artKey = ""
	if m.width < 96 || m.mode == modeHelp {
		return nil
	}
	req := m.artworkRequestAt(m.cursor)
	if req.key == "" {
		return m.prefetchNextArt()
	}
	m.artKey = req.key
	if _, ok := m.art[req.key]; ok {
		return m.prefetchNextArt()
	}
	if m.artPending[req.key] {
		return nil
	}
	m.artTick++
	tick := m.artTick
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg {
		return artTickMsg{
			key: req.key, tick: tick, pic: req.pic, artist: req.artist, album: req.album,
		}
	})
}

func (m model) artworkRequestAt(i int) artRequest {
	if m.mode == modeHome {
		items := m.chartItems()
		if i < 0 || i >= len(items) {
			return artRequest{}
		}
		item := items[i]
		pic := itunesArtworkSize.ReplaceAllString(item.ArtworkURL, "/600x600bb")
		album := item.Name
		key := pic
		if key == "" {
			key = artworkKey(track{Artist: item.Artist, AlbumName: album})
		}
		return artRequest{key: key, pic: pic, artist: item.Artist, album: album}
	}
	if i < 0 || i >= len(m.tracks) {
		return artRequest{}
	}
	t := m.tracks[i]
	return artRequest{
		key: artworkKey(t), pic: t.PicURL, artist: t.Artist, album: t.AlbumName,
	}
}

func (m model) artworkRequests() []artRequest {
	start := max(0, m.offset)
	end := min(m.itemCount(), start+m.bodyHeight())
	requests := make([]artRequest, 0, max(0, end-start))
	for i := start; i < end; i++ {
		requests = append(requests, m.artworkRequestAt(i))
	}
	return requests
}

func (m *model) prefetchNextArt() tea.Cmd {
	if m.width < 96 || m.mode == modeHelp || m.artPrefetchActive {
		return nil
	}
	requests := m.artworkRequests()
	if len(requests) == 0 {
		return nil
	}
	cursor := m.cursor - m.offset
	if cursor < 0 || cursor >= len(requests) {
		cursor = 0
	}
	for offset := 1; offset <= len(requests); offset++ {
		i := (cursor + offset) % len(requests)
		req := requests[i]
		if req.key == "" || m.art[req.key] != "" ||
			m.artPending[req.key] || m.artPrefetched[req.key] {
			continue
		}
		m.artPrefetched[req.key] = true
		m.artPending[req.key] = true
		m.artPrefetchActive = true
		return artCmd(req, true, artColumns)
	}
	return nil
}

func (m *model) cycleQuality() {
	switch m.quality {
	case "auto":
		m.quality = "flac"
	case "flac":
		m.quality = "320"
	case "320":
		m.quality = "128"
	default:
		m.quality = "auto"
	}
}

func (m *model) fireToast(text string) tea.Cmd {
	m.toastText = text
	m.toastID++
	id := m.toastID
	return tea.Tick(4*time.Second, func(time.Time) tea.Msg { return toastMsg{id: id} })
}

func (m model) current() *track {
	if m.cursor < 0 || m.cursor >= len(m.tracks) {
		return nil
	}
	return &m.tracks[m.cursor]
}

func (m model) bodyHeight() int {
	h := m.height - 5
	if h < 3 {
		h = 3
	}
	return h
}

func (m model) pageStep() int {
	if h := m.bodyHeight() / 2; h > 0 {
		return h
	}
	return 1
}

func qualityName(q string) string {
	switch q {
	case "flac":
		return "FLAC"
	case "320":
		return "320k"
	case "128":
		return "128k"
	}
	return "auto"
}
