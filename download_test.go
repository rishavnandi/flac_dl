package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestFileNameSanitizes(t *testing.T) {
	cases := []struct{ artist, name, want string }{
		{"AC/DC", `Back:In "Black"`, "AC_DC - Back_In _Black_.flac"},
		{"周杰伦", "晴天", "周杰伦 - 晴天.flac"},
		{"Daft Punk", "  The   Grid  ", "Daft Punk - The Grid.flac"},
		{"", "", "track 7.flac"},
	}
	for _, tc := range cases {
		got := fileName(track{ID: "7", Artist: tc.artist, Name: tc.name}, ".flac")
		if got != tc.want {
			t.Fatalf("got %q, want %q", got, tc.want)
		}
	}
}

func TestTruncateBytesKeepsRunes(t *testing.T) {
	got := truncateBytes(strings.Repeat("无", 100), 100)
	if len(got) > 100 {
		t.Fatalf("too long: %d bytes", len(got))
	}
	if len(got) != 99 || !utf8.ValidString(got) {
		t.Fatalf("cut mid rune: %d bytes, valid %v", len(got), utf8.ValidString(got))
	}
}

func TestPickQuality(t *testing.T) {
	full := track{Minfo: []quality{
		{Format: "flac", Bitrate: "2000"},
		{Format: "mp3", Bitrate: "320"},
		{Format: "mp3", Bitrate: "128"},
	}}
	for want, format := range map[string]string{"auto": "flac", "flac": "flac", "320": "mp3", "128": "mp3"} {
		q, err := pickQuality(full, want)
		if err != nil {
			t.Fatalf("%s: %v", want, err)
		}
		if q.Format != format {
			t.Fatalf("%s: got %s", want, q.Format)
		}
	}

	if q, err := pickQuality(full, "320"); err != nil || q.Bitrate != "320" {
		t.Fatalf("320 picked %v, %v", q, err)
	}

	mp3Only := track{Minfo: []quality{{Format: "mp3", Bitrate: "128"}}}
	if _, err := pickQuality(mp3Only, "flac"); err == nil || !strings.Contains(err.Error(), "available") {
		t.Fatalf("want a listing of available formats, got %v", err)
	}
	if q, err := pickQuality(mp3Only, "auto"); err != nil || q.Format != "mp3" {
		t.Fatalf("auto fallback: %v, %v", q, err)
	}
}

func TestGetWritesFileAndCleansPart(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "audio bytes")
	}))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "a.flac")
	d := &downloader{http: srv.Client()}
	if err := d.get(srv.URL+"/x.flac", path); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "audio bytes" {
		t.Fatalf("got %q", body)
	}
	if _, err := os.Stat(path + ".part"); !os.IsNotExist(err) {
		t.Fatalf(".part left behind: %v", err)
	}
}

func TestGetRejectsHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gone", http.StatusForbidden)
	}))
	defer srv.Close()

	path := filepath.Join(t.TempDir(), "a.flac")
	if err := (&downloader{http: srv.Client()}).get(srv.URL+"/x.flac", path); err == nil {
		t.Fatal("want an error on 403")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("file written on failure")
	}
}
