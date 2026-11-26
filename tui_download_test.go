//go:build !vercel

package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestAlbumDownloadRunsQueueAndReportsSkipped(t *testing.T) {
	var active, maxActive atomic.Int32
	audio := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		now := active.Add(1)
		for old := maxActive.Load(); now > old && !maxActive.CompareAndSwap(old, now); old = maxActive.Load() {
		}
		time.Sleep(40 * time.Millisecond)
		active.Add(-1)
		w.Header().Set("Content-Length", "4")
		_, _ = w.Write([]byte("data"))
	}))
	defer audio.Close()

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		var data any
		switch r.URL.Query().Get("act") {
		case "getUrl":
			data = map[string]string{"url": audio.URL + "/" + r.PostForm.Get("songid"), "songid": r.PostForm.Get("songid")}
		default:
			t.Errorf("unexpected action %q", r.URL.Query().Get("act"))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": data})
	}))
	defer api.Close()

	c := &client{base: api.URL, http: api.Client()}
	dir := t.TempDir()
	jobs := make([]job, 5)
	for i := range jobs {
		id := fmt.Sprint(i + 1)
		jobs[i] = job{
			platform: "kuwo",
			track: track{
				ID: loose(id), Name: "track " + id, Artist: "artist",
				Time: 1, Sign: "sig",
			},
			quality: quality{Format: "mp3", Bitrate: "128"},
			dir:     dir,
		}
	}

	ch := make(chan tea.Msg, 64)
	startAlbumDownload(c, jobs, 2, ch)()
	var itemDone int
	var result albumDoneMsg
	deadline := time.After(5 * time.Second)
	for {
		select {
		case msg := <-ch:
			switch msg := msg.(type) {
			case itemDoneMsg:
				itemDone++
			case albumDoneMsg:
				result = msg
				goto complete
			}
		case <-deadline:
			t.Fatal("album download did not finish")
		}
	}

complete:
	if itemDone != len(jobs) {
		t.Fatalf("finished %d jobs, want %d", itemDone, len(jobs))
	}
	if result.failed != 0 || result.total != len(jobs) || result.skipped != 2 {
		t.Fatalf("unexpected summary: %#v", result)
	}
	if maxActive.Load() < 2 {
		t.Fatalf("only %d downloads ran at once, want parallel workers", maxActive.Load())
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != len(jobs) {
		t.Fatalf("wrote %d files, want %d", len(files), len(jobs))
	}
	for _, file := range files {
		if filepath.Ext(file.Name()) != ".mp3" {
			t.Fatalf("unexpected output %q", file.Name())
		}
	}
}
