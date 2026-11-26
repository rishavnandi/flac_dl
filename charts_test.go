package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAppleChartClientFetchesSongsAndAlbums(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/songs.json":
			_ = json.NewEncoder(w).Encode(appleChartResponse{Feed: appleChartFeed{
				Title: "Top Songs", Country: "us", Updated: "today",
				Results: []appleChartItem{{
					Name: "Song", Artist: "Artist",
					ArtworkURL: "https://example.com/100x100bb.jpg",
				}},
			}})
		case "/albums.json":
			_ = json.NewEncoder(w).Encode(appleChartResponse{Feed: appleChartFeed{
				Title: "Top Albums", Results: []appleChartItem{{Name: "Album"}},
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	c := &appleChartClient{base: server.URL, http: server.Client()}
	songs, err := c.fetch("songs")
	if err != nil {
		t.Fatal(err)
	}
	if songs.Title != "Top Songs" || songs.Country != "us" || len(songs.Results) != 1 ||
		songs.Results[0].Name != "Song" || songs.Results[0].ArtworkURL == "" {
		t.Fatalf("unexpected songs feed: %#v", songs)
	}

	albums, err := c.fetch("albums")
	if err != nil {
		t.Fatal(err)
	}
	if albums.Title != "Top Albums" || len(albums.Results) != 1 || albums.Results[0].Name != "Album" {
		t.Fatalf("unexpected albums feed: %#v", albums)
	}
}

func TestChartCacheCoalescesRequestsAndSurvivesRestart(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_ = json.NewEncoder(w).Encode(appleChartResponse{Feed: appleChartFeed{
			Title: r.URL.Path, Results: []appleChartItem{{Name: "Song"}},
		}})
	}))
	defer server.Close()
	c := &appleChartClient{base: server.URL, http: server.Client(), cacheDir: t.TempDir()}
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			feed, err := c.cachedFetchContext(context.Background(), "songs")
			if err != nil || len(feed.Results) != 1 {
				t.Errorf("cached fetch: %#v, %v", feed, err)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("got %d upstream calls, want 1", calls.Load())
	}
	restarted := &appleChartClient{base: server.URL, http: server.Client(), cacheDir: c.cacheDir}
	if _, err := restarted.cachedFetchContext(context.Background(), "songs"); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("restart ignored the disk cache")
	}
	if _, err := restarted.cachedFetchContext(context.Background(), "albums"); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatal("albums did not use a separate cache")
	}
}

func TestChartCacheServesStaleFeedDuringTimeout(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()
	c := &appleChartClient{base: server.URL, http: &http.Client{Timeout: 100 * time.Millisecond}}
	entry := &appleChartCache{
		feed:    appleChartFeed{Title: "Last good", Results: []appleChartItem{{Name: "Song"}}},
		checked: time.Now().Add(-time.Hour),
	}
	c.cache = map[string]*appleChartCache{"songs": entry}
	feed, err := c.cachedFetchContext(context.Background(), "songs")
	if err != nil || feed.Title != "Last good" {
		t.Fatalf("stale fetch: %#v, %v", feed, err)
	}
	<-started
	c.mu.Lock()
	pending := entry.pending
	c.mu.Unlock()
	<-pending
	c.mu.Lock()
	refreshErr := entry.err
	c.mu.Unlock()
	if refreshErr == nil {
		t.Fatal("expected upstream timeout")
	}
	feed, err = c.cachedFetchContext(context.Background(), "songs")
	if err != nil || feed.Title != "Last good" {
		t.Fatalf("timeout discarded the last good feed: %#v, %v", feed, err)
	}
}

func TestChartCacheRecoversCorruptDiskAndBacksOffFailures(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "songs.json"), []byte(`{"feed":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c := &appleChartClient{base: server.URL, http: server.Client(), cacheDir: dir}
	for range 3 {
		if _, err := c.cachedFetchContext(context.Background(), "songs"); err == nil {
			t.Fatal("expected upstream error, not an empty cached feed")
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("failure caused %d requests instead of backing off", calls.Load())
	}
}

func TestAppleChartClientRejectsUnknownKind(t *testing.T) {
	c := &appleChartClient{base: "://bad", http: http.DefaultClient}
	if _, err := c.fetch("artists"); err == nil {
		t.Fatal("expected unsupported chart error")
	}
}
