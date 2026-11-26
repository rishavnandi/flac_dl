package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testWebApp(t *testing.T, api func(string, url.Values) string) (*webApp, *httptest.Server) {
	t.Helper()
	c, _, _ := testClient(t, api)
	chartsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"feed":{"title":"US Top Songs","country":"us","updated":"2026-11-26T00:00:00Z","results":[{"name":"Test Song","artistName":"Test Artist","artworkUrl100":"https://is.example/test/100x100bb.jpg"}]}}`)
	}))
	t.Cleanup(chartsServer.Close)
	app, err := newWebApp(c, &appleChartClient{
		base: strings.TrimRight(chartsServer.URL, "/"),
		http: chartsServer.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return app, chartsServer
}

func webTestRequest(method, target string, body io.Reader) *http.Request {
	if strings.HasPrefix(target, "/") {
		target = "http://127.0.0.1:8765" + target
	}
	return httptest.NewRequest(method, target, body)
}

func TestWebChartsAndStaticAssets(t *testing.T) {
	app, _ := testWebApp(t, func(string, url.Values) string {
		return `{"code":0,"data":{"list":[]}}`
	})
	handler := app.handler()

	res := httptest.NewRecorder()
	handler.ServeHTTP(res, webTestRequest(http.MethodGet, "/", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("home status = %d", res.Code)
	}
	if !strings.Contains(res.Body.String(), `id="player-title" data-action="expand-player" aria-haspopup="dialog"`) {
		t.Fatal("player title does not open the full-screen dialog")
	}
	for _, want := range []string{"Discover", "Test Song", "https://is.example/test/600x600bb.jpg", "/static/site.css", `id="player-dock" class="player-dock" hx-preserve`, `class="brand" href="/" hx-get="/" hx-target="#view" hx-push-url="true"`, `id="view" class="view" hx-history-elt`, `id="fullscreen-player"`, `aria-label="Open full-screen player"`} {
		if !strings.Contains(res.Body.String(), want) {
			t.Errorf("home page missing %q", want)
		}
	}

	res = httptest.NewRecorder()
	req := webTestRequest(http.MethodGet, "/charts/albums", nil)
	req.Header.Set("HX-Request", "true")
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK || strings.Contains(res.Body.String(), "<!doctype html>") || !strings.Contains(res.Body.String(), "Top albums") {
		t.Fatalf("HTMX chart response = %d, %q", res.Code, res.Body.String())
	}

	res = httptest.NewRecorder()
	req.Header.Set("HX-History-Restore-Request", "true")
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `id="view" class="view" hx-history-elt`) || !strings.Contains(res.Body.String(), "<!doctype html>") {
		t.Fatalf("history restore needs a full page with the history element: %d, %q", res.Code, res.Body.String())
	}

	res = httptest.NewRecorder()
	handler.ServeHTTP(res, webTestRequest(http.MethodGet, "/static/htmx.min.js", nil))
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `version:"2.0.8"`) {
		t.Fatalf("HTMX asset response = %d, %q", res.Code, res.Body.String())
	}
}

func TestWebSearchEscapesTrackMetadata(t *testing.T) {
	app, _ := testWebApp(t, func(act string, _ url.Values) string {
		if act != "search" {
			return `{"code":0,"data":{"list":[]}}`
		}
		return fmt.Sprintf(`{"code":0,"data":{"total":1,"list":[%s]}}`, trackJSON(42, `<script>alert(1)</script>`))
	})
	res := httptest.NewRecorder()
	app.handler().ServeHTTP(res, webTestRequest(http.MethodGet, "/search?q=test", nil))
	body := res.Body.String()
	if res.Code != http.StatusOK {
		t.Fatalf("search status = %d", res.Code)
	}
	if strings.Contains(body, "<script>alert(1)</script>") || !strings.Contains(body, "&lt;script&gt;") {
		t.Fatal("track metadata was not HTML-escaped")
	}
	if !strings.Contains(body, `data-fallback="/artwork?`) {
		t.Fatal("track artwork fallback was not rendered")
	}
	if !strings.Contains(body, `class="track-title" data-action="play-title"`) {
		t.Fatal("song titles are not keyboard-accessible playback buttons")
	}
}

func TestWebStreamResolvesAndRedirects(t *testing.T) {
	var resolved url.Values
	app, _ := testWebApp(t, func(act string, form url.Values) string {
		switch act {
		case "song":
			return `{"code":0,"data":{"list":[` + trackJSON(42, "Test Song") + `]}}`
		case "getUrl":
			resolved = form
			return `{"code":0,"data":{"url":"https://cdn.example/test.flac?token=abc","songid":42}}`
		default:
			return `{"code":0,"data":{"list":[]}}`
		}
	})
	res := httptest.NewRecorder()
	app.handler().ServeHTTP(res, webTestRequest(http.MethodGet, "/stream?id=42&q=flac", nil))
	if res.Code != http.StatusFound || res.Header().Get("Location") != "https://cdn.example/test.flac?token=abc" {
		t.Fatalf("stream response = %d %q", res.Code, res.Header().Get("Location"))
	}
	if resolved.Get("format") != "flac" {
		t.Fatalf("resolved format = %q", resolved.Get("format"))
	}
}

func TestWebStreamDoesNotRedirectToSpokenNotice(t *testing.T) {
	app, _ := testWebApp(t, func(act string, _ url.Values) string {
		if act == "song" {
			return `{"code":0,"data":{"list":[` + trackJSON(42, "Test Song") + `]}}`
		}
		return `{"code":0,"data":{"url":"https://cdn.example/notice.mp3","songid":661178187}}`
	})
	res := httptest.NewRecorder()
	app.handler().ServeHTTP(res, webTestRequest(http.MethodGet, "/stream?id=42&q=auto", nil))
	if res.Code != http.StatusBadGateway || res.Header().Get("Location") != "" {
		t.Fatalf("substitute notice reached player: %d %q", res.Code, res.Header().Get("Location"))
	}
}

func TestHostedSiteUsesBrowserDownloadsAndRestrictsHosts(t *testing.T) {
	app, _ := testWebApp(t, func(act string, _ url.Values) string {
		if act == "search" {
			return `{"code":0,"data":{"total":1,"list":[` + trackJSON(42, "Test Song") + `]}}`
		}
		return `{"code":0,"data":{"info":{"name":"Test Album"},"list":[` + trackJSON(42, "Test Song") + `]}}`
	})
	app.publicHosts = map[string]bool{"music.vercel.app": true}
	for _, target := range []string{"/search?q=test", "/album?id=215821"} {
		res := httptest.NewRecorder()
		app.handler().ServeHTTP(res, webTestRequest(http.MethodGet, "https://music.vercel.app"+target, nil))
		body := res.Body.String()
		if res.Code != http.StatusOK || !strings.Contains(body, `browser-download`) || strings.Contains(body, `hx-post="/download"`) || strings.Contains(body, `id="lyrics"`) {
			t.Fatalf("hosted page is not browser-only: %d, %q", res.Code, body)
		}
	}
	for _, host := range []string{"attacker.example", "127.0.0.1:8765"} {
		res := httptest.NewRecorder()
		app.handler().ServeHTTP(res, webTestRequest(http.MethodGet, "https://"+host+"/", nil))
		if res.Code != http.StatusForbidden {
			t.Fatalf("host %q status = %d", host, res.Code)
		}
	}
	res := httptest.NewRecorder()
	app.handler().ServeHTTP(res, webTestRequest(http.MethodPost, "https://music.vercel.app/download", nil))
	if res.Code != http.StatusMethodNotAllowed || len(app.downloads.records) != 0 {
		t.Fatal("hosted site accepted a background disk download")
	}
}

type webTestTransport func(*http.Request) (*http.Response, error)

func (f webTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestHostedDownloadStreamsLargeFileWithSafeFilename(t *testing.T) {
	app, _ := testWebApp(t, func(act string, _ url.Values) string {
		if act == "song" {
			return `{"code":0,"data":{"list":[` + trackJSON(42, "Test Song") + `]}}`
		}
		return `{"code":0,"data":{"url":"https://other-lv.kuwo.cn/song.flac","songid":42}}`
	})
	app.publicHosts = map[string]bool{"music.vercel.app": true}
	content := strings.Repeat("audio", 1<<20)
	app.client.http.Transport = webTestTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Hostname() == "other-lv.kuwo.cn" {
			return &http.Response{
				StatusCode: http.StatusOK, Header: make(http.Header),
				Body: io.NopCloser(strings.NewReader(content)), ContentLength: int64(len(content)),
			}, nil
		}
		return http.DefaultTransport.RoundTrip(r)
	})
	res := httptest.NewRecorder()
	app.handler().ServeHTTP(res, webTestRequest(http.MethodGet, "https://music.vercel.app/stream?id=42&q=flac&download=1", nil))
	if res.Code != http.StatusOK || !res.Flushed || res.Body.String() != content {
		t.Fatalf("streamed download = %d, flushed %v, %d bytes", res.Code, res.Flushed, res.Body.Len())
	}
	if disposition := res.Header().Get("Content-Disposition"); !strings.Contains(disposition, `attachment; filename="Daft Punk - Test Song.flac"`) {
		t.Fatalf("download disposition = %q", disposition)
	}
}

func TestHostedDownloadRejectsUntrustedSourceAndRedirect(t *testing.T) {
	for _, target := range []string{"https://127.0.0.1/private", "https://other-lv.kuwo.cn/song.flac"} {
		t.Run(target, func(t *testing.T) {
			app, _ := testWebApp(t, func(act string, _ url.Values) string {
				if act == "song" {
					return `{"code":0,"data":{"list":[` + trackJSON(42, "Test Song") + `]}}`
				}
				return fmt.Sprintf(`{"code":0,"data":{"url":%q,"songid":42}}`, target)
			})
			app.publicHosts = map[string]bool{"music.vercel.app": true}
			app.client.http.Transport = webTestTransport(func(r *http.Request) (*http.Response, error) {
				if r.URL.Hostname() == "127.0.0.1" && r.URL.Path == "/private" {
					t.Fatal("download attempted to access a private address")
				}
				if r.URL.Hostname() == "other-lv.kuwo.cn" {
					return &http.Response{
						StatusCode: http.StatusFound, Header: http.Header{"Location": {"https://127.0.0.1/private"}},
						Body: io.NopCloser(strings.NewReader("")),
					}, nil
				}
				return http.DefaultTransport.RoundTrip(r)
			})
			res := httptest.NewRecorder()
			app.handler().ServeHTTP(res, webTestRequest(http.MethodGet, "https://music.vercel.app/stream?id=42&q=flac&download=1", nil))
			if res.Code != http.StatusBadGateway {
				t.Fatalf("untrusted download status = %d", res.Code)
			}
		})
	}
}

func TestWebDownloadRejectsCrossOrigin(t *testing.T) {
	app, _ := testWebApp(t, func(string, url.Values) string {
		t.Fatal("cross-origin request reached the API")
		return ""
	})
	req := webTestRequest(http.MethodPost, "/download", strings.NewReader("track_id=42"))
	req.Header.Set("Origin", "https://attacker.example")
	res := httptest.NewRecorder()
	app.handler().ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("cross-origin download status = %d", res.Code)
	}

	req = webTestRequest(http.MethodPost, "/download", strings.NewReader("track_id=not-an-id"))
	req.Header.Set("Origin", "http://127.0.0.1:8765")
	res = httptest.NewRecorder()
	app.handler().ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("same-origin invalid download status = %d, want 400", res.Code)
	}
}

func TestWebRejectsNonLoopbackHostHeader(t *testing.T) {
	app, _ := testWebApp(t, func(string, url.Values) string {
		t.Fatal("untrusted Host reached an upstream service")
		return ""
	})
	req := httptest.NewRequest(http.MethodGet, "http://attacker.example/", nil)
	res := httptest.NewRecorder()
	app.handler().ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("untrusted Host status = %d", res.Code)
	}
}

func TestWebDownloadManagerTracksExistingFile(t *testing.T) {
	dir := t.TempDir()
	item := job{
		track:   track{ID: "42", Name: "Existing Song", Artist: "Test Artist"},
		quality: quality{Format: "mp3"},
		dir:     dir,
	}
	if err := os.WriteFile(filepath.Join(dir, fileName(item.track, extOf(item.quality))), []byte("done"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := &webDownloads{records: make(map[string]*webDownloadRecord)}
	id := manager.start(&client{}, "Existing Song", []job{item}, 0)
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		record, ok := manager.get(id)
		if !ok {
			t.Fatal("download record disappeared")
		}
		if record.State != "active" {
			if record.State != "done" || record.Finished != 1 || record.Skipped != 1 {
				t.Fatalf("download record = %#v", record.webDownloadView)
			}
			break
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatal("download task did not finish")
		}
	}
}
