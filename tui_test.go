//go:build !vercel

package main

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestTUIViewFitsTerminal(t *testing.T) {
	m := newModel()
	m.width, m.height = 110, 32
	m.mode = modeResults
	m.input = textinput.New()
	m.query = "tron legacy"
	m.total = 1
	m.tracks = []track{{
		ID: "42", Name: "The Grid", Artist: "Daft Punk",
		AlbumName: "TRON Legacy", PicURL: "https://example.com/cover.png",
		Duration: "96", Minfo: []quality{{Format: "flac", Bitrate: "2000"}},
	}}

	lines := strings.Split(m.View(), "\n")
	if len(lines) != m.height {
		t.Fatalf("view is %d rows, want %d", len(lines), m.height)
	}
	for i, line := range lines {
		if got := lipgloss.Width(line); got != m.width {
			t.Fatalf("row %d is %d cells wide, want %d", i, got, m.width)
		}
	}
}

func TestChartHomeFitsTerminal(t *testing.T) {
	m := newModel()
	m.width, m.height = 110, 32
	m.chartData["songs"] = appleChartFeed{Results: []appleChartItem{{
		Name: "Choosin' Texas", Artist: "Ella Langley",
		ArtworkURL: "https://example.com/100x100bb.jpg", ReleaseDate: "2025-10-17",
	}}}

	lines := strings.Split(m.View(), "\n")
	if len(lines) != m.height {
		t.Fatalf("view is %d rows, want %d", len(lines), m.height)
	}
	for i, line := range lines {
		if got := lipgloss.Width(line); got != m.width {
			t.Fatalf("row %d is %d cells wide, want %d", i, got, m.width)
		}
	}
}

func TestHomeStartsOnChartsAndHelpReturnsHome(t *testing.T) {
	m := newModel()
	if m.mode != modeHome || m.focusSearch {
		t.Fatalf("new TUI should show charts with search unfocused, got mode=%v focused=%v", m.mode, m.focusSearch)
	}
	updated, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	m = updated.(model)
	if m.mode != modeHelp || m.helpReturn != modeHome {
		t.Fatalf("help did not preserve the home screen: mode %v return %v", m.mode, m.helpReturn)
	}

	updated, _ = m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	m = updated.(model)
	if m.mode != modeHome {
		t.Fatalf("help did not return home: mode %v", m.mode)
	}
}

func TestEnterOnChartItemSearchesKuwo(t *testing.T) {
	m := newModel()
	m.chartData["songs"] = appleChartFeed{Results: []appleChartItem{
		{Name: "Song One", Artist: "Artist"},
		{Name: "Song Two", Artist: "Another Artist"},
	}}
	updated, _ := m.handleKey(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(model)
	if m.cursor != 1 {
		t.Fatalf("selected chart row %d, want 1", m.cursor)
	}
	updated, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(model)
	if cmd == nil || m.mode != modeResults || m.query != "Song Two Another Artist" || m.searchReturn != modeHome {
		t.Fatalf("chart selection did not start a Kuwo search: mode=%v query=%q return=%v", m.mode, m.query, m.searchReturn)
	}
}

func TestPlaybackCanBeCanceledWhileLoading(t *testing.T) {
	m := newModel()
	m.mode = modeResults
	m.tracks = []track{{
		ID: "42", Name: "song", Artist: "artist",
		Minfo: []quality{{Format: "flac", Bitrate: "2000"}},
	}}
	updated, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeySpace})
	m = updated.(model)
	if cmd == nil || m.playbackStatus != "loading" {
		t.Fatal("playback did not enter loading state")
	}
	seq := m.playbackSeq
	m.stopPlayback()
	if m.playbackStatus != "" || m.playbackSeq == seq {
		t.Fatal("stop did not cancel the pending playback")
	}
	msg, ok := cmd().(playbackReadyMsg)
	if !ok || !errors.Is(msg.err, context.Canceled) {
		t.Fatalf("late playback result = %#v, want cancellation", msg)
	}
	updated, _ = m.Update(msg)
	if got := updated.(model).playbackStatus; got != "" {
		t.Fatalf("stale result restored playback status %q", got)
	}
}

func TestFailedCoverCanRetryLater(t *testing.T) {
	m := newModel()
	m.width = 110
	m.mode = modeResults
	m.tracks = []track{{PicURL: "https://example.com/cover.png"}}
	m.artKey = "https://example.com/cover.png"
	updated, cmd := m.Update(artMsg{key: m.artKey, err: errors.New("temporary network error")})
	m = updated.(model)
	if _, cached := m.art[m.artKey]; cached {
		t.Fatal("failed cover was cached and cannot be retried")
	}
	if cmd == nil || m.artRetries[m.artKey] != 1 {
		t.Fatalf("automatic retry not scheduled: command=%v attempts=%d", cmd != nil, m.artRetries[m.artKey])
	}
	updated, cmd = m.Update(artRetryMsg{key: m.artKey})
	m = updated.(model)
	if cmd == nil {
		t.Fatal("retry did not schedule another cover fetch")
	}
}

func TestCoverPrefetchFillsSearchAlbumAndChartListsSequentially(t *testing.T) {
	for _, mode := range []viewMode{modeHome, modeResults, modeAlbum} {
		t.Run(map[viewMode]string{modeHome: "charts", modeResults: "search", modeAlbum: "album"}[mode], func(t *testing.T) {
			m := newModel()
			m.width, m.mode = 110, mode
			if mode == modeHome {
				m.chartData["songs"] = appleChartFeed{Results: []appleChartItem{
					{Name: "one", Artist: "artist", ArtworkURL: "https://example.com/one/100x100bb.jpg"},
					{Name: "two", Artist: "artist", ArtworkURL: "https://example.com/two/100x100bb.jpg"},
					{Name: "three", Artist: "artist", ArtworkURL: "https://example.com/three/100x100bb.jpg"},
				}}
			} else {
				m.tracks = []track{
					{Name: "one", PicURL: "https://example.com/one.png"},
					{Name: "two", PicURL: "https://example.com/two.png"},
					{Name: "three", PicURL: "https://example.com/three.png"},
				}
			}
			first := m.artworkRequestAt(0).key
			second := m.artworkRequestAt(1).key
			third := m.artworkRequestAt(2).key
			m.artKey = first

			updated, cmd := m.Update(artMsg{key: first, art: "first"})
			m = updated.(model)
			if cmd == nil || !m.artPending[second] {
				t.Fatal("first cover did not start the next prefetch")
			}
			updated, cmd = m.Update(artMsg{key: second, art: "second", prefetch: true})
			m = updated.(model)
			if cmd == nil || !m.artPending[third] {
				t.Fatal("prefetch did not advance to the following cover")
			}
		})
	}
}

func TestCoverPrefetchOnlyUsesVisibleRows(t *testing.T) {
	m := newModel()
	m.width, m.height, m.mode = 110, 10, modeResults
	m.tracks = make([]track, 8)
	for i := range m.tracks {
		m.tracks[i].PicURL = "https://example.com/" + strconv.Itoa(i) + ".png"
	}
	if got := len(m.artworkRequests()); got != m.bodyHeight() {
		t.Fatalf("prefetch list has %d rows, want visible height %d", got, m.bodyHeight())
	}
}
