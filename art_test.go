package main

import (
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestCoverURLPreservesKuwoImageSize(t *testing.T) {
	got := coverURL("http://img1.kuwo.cn/star/albumcover/120/s4s25/37/cover.png")
	want := "https://img1.kuwo.cn/star/albumcover/120/s4s25/37/cover.png"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestItunesArtworkURLRequiresExactArtistAndAlbum(t *testing.T) {
	oldEndpoint := itunesSearchURL
	oldClient := artHTTP
	defer func() {
		itunesSearchURL = oldEndpoint
		artHTTP = oldClient
	}()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("entity") != "album" || r.URL.Query().Get("limit") != "5" {
			t.Errorf("unexpected query: %v", r.URL.Query())
		}
		_ = json.NewEncoder(w).Encode(itunesResponse{Results: []struct {
			ArtistName    string `json:"artistName"`
			Collection    string `json:"collectionName"`
			ArtworkURL100 string `json:"artworkUrl100"`
		}{
			{ArtistName: "Other Artist", Collection: "TRON: Legacy", ArtworkURL100: "https://example.com/wrong-artist/100x100bb.jpg"},
			{ArtistName: "Daft Punk", Collection: "TRON Legacy Reconfigured", ArtworkURL100: "https://example.com/wrong-album/100x100bb.jpg"},
			{ArtistName: "Daft Punk", Collection: "TRON: Legacy", ArtworkURL100: "https://example.com/right/100x100bb.jpg"},
		}})
	}))
	defer server.Close()
	artHTTP = server.Client()
	itunesSearchURL = server.URL + "/search"

	got, err := itunesArtworkURL("Daft Punk", "TRON: Legacy")
	if err != nil {
		t.Fatal(err)
	}
	want := "https://example.com/right/600x600bb.jpg"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestRenderArtUsesRequestedCellGrid(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 30), G: uint8(y * 30), A: 255})
		}
	}

	lines := strings.Split(renderArt(img, 8), "\n")
	if len(lines) != 4 {
		t.Fatalf("got %d rows, want 4", len(lines))
	}
	for i, line := range lines {
		if got := lipgloss.Width(line); got != 8 {
			t.Fatalf("row %d is %d cells wide, want 8", i, got)
		}
	}
}

func TestFetchArtDecodesImageResponse(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 30), G: uint8(y * 30), A: 255})
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		if err := png.Encode(w, img); err != nil {
			t.Errorf("encode image: %v", err)
		}
	}))
	defer srv.Close()

	oldClient := artHTTP
	artHTTP = srv.Client()
	defer func() { artHTTP = oldClient }()

	got, err := fetchArt(srv.URL, 8)
	if err != nil {
		t.Fatal(err)
	}
	if rows := strings.Count(got, "\n") + 1; rows != 4 {
		t.Fatalf("got %d rows, want 4", rows)
	}
}

func TestFetchCoverFallsBackToItunes(t *testing.T) {
	oldEndpoint := itunesSearchURL
	oldClient := artHTTP
	defer func() {
		itunesSearchURL = oldEndpoint
		artHTTP = oldClient
	}()

	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	imgServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/kuwo-fail" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		if err := png.Encode(w, img); err != nil {
			t.Errorf("encode image: %v", err)
		}
	}))
	defer imgServer.Close()

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"results": []map[string]string{{
				"artistName": "Daft Punk", "collectionName": "TRON: Legacy",
				"artworkUrl100": imgServer.URL + "/100x100bb.jpg",
			}},
		})
	}))
	defer api.Close()
	artHTTP = api.Client()
	itunesSearchURL = api.URL + "/search"

	got, err := fetchCover(imgServer.URL+"/kuwo-fail", "Daft Punk", "TRON: Legacy", 8)
	if err != nil {
		t.Fatal(err)
	}
	if rows := strings.Count(got, "\n") + 1; rows != 4 {
		t.Fatalf("got %d rows, want 4", rows)
	}
}
