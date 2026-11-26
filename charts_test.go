package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
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

func TestAppleChartClientRejectsUnknownKind(t *testing.T) {
	c := &appleChartClient{base: "://bad", http: http.DefaultClient}
	if _, err := c.fetch("artists"); err == nil {
		t.Fatal("expected unsupported chart error")
	}
}
