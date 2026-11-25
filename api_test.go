package main

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// testClient serves canned responses and records every call.
func testClient(t *testing.T, h func(act string, form url.Values) string) (*client, *[]string, *[]url.Values) {
	t.Helper()
	acts := []string{}
	forms := []url.Values{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		acts = append(acts, r.URL.Query().Get("act"))
		forms = append(forms, r.PostForm)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		io.WriteString(w, h(r.URL.Query().Get("act"), r.PostForm))
	}))
	t.Cleanup(srv.Close)
	return &client{base: srv.URL, http: srv.Client()}, &acts, &forms
}

func trackJSON(id int, name string) string {
	return fmt.Sprintf(`{"id":%d,"name":%q,"artist":"Daft Punk","album_name":"A","album_id":"215821","duration":"197","minfo":[{"level":"ff","bitrate":"2000","format":"flac","size":"40.42Mb"},{"level":"p","bitrate":"320","format":"mp3","size":"7.55Mb"}],"time":1795630934,"sign":"sig%d"}`, id, name, id)
}

func TestSearchParsesMixedTypes(t *testing.T) {
	c, acts, forms := testClient(t, func(string, url.Values) string {
		return `{"code":0,"msg":"ok","data":{"total":1,"list":[` + trackJSON(2407050, "TRON Legacy") + `]}}`
	})

	tracks, total, err := c.search("kuwo", "tron legacy", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 1 || total != 1 {
		t.Fatalf("got %d tracks, total %d", len(tracks), total)
	}
	got := tracks[0]
	if got.ID != "2407050" || got.Duration != "197" || got.AlbumID != "215821" {
		t.Fatalf("loose fields not decoded: %#v", got)
	}
	if got.Time != 1795630934 || got.Sign == "" {
		t.Fatalf("signature not decoded: %#v", got)
	}
	if got.Minfo[0].Bitrate != "2000" || got.Minfo[0].Format != "flac" {
		t.Fatalf("quality not decoded: %#v", got.Minfo)
	}
	if (*acts)[0] != "search" || (*forms)[0].Get("keyword") != "tron legacy" {
		t.Fatalf("wrong request: act %q form %v", (*acts)[0], (*forms)[0])
	}
}

func TestSearchPagesUntilLimit(t *testing.T) {
	c, acts, _ := testClient(t, func(_ string, form url.Values) string {
		n := 0
		switch form.Get("page") {
		case "1", "2":
			n = 20
		case "3":
			n = 5
		}
		items := make([]string, n)
		for i := range items {
			items[i] = trackJSON(i+1, "track")
		}
		return fmt.Sprintf(`{"code":0,"data":{"total":45,"list":[%s]}}`, strings.Join(items, ","))
	})

	tracks, total, err := c.search("kuwo", "x", 45)
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 45 || total != 45 {
		t.Fatalf("got %d tracks, total %d", len(tracks), total)
	}
	if len(*acts) != 3 {
		t.Fatalf("want 3 pages, made %d calls", len(*acts))
	}
}

func TestSearchSurfacesAPICode(t *testing.T) {
	c, _, _ := testClient(t, func(string, url.Values) string {
		return `{"code":-2,"msg":"参数校验失败"}`
	})

	_, _, err := c.search("kuwo", "x", 5)
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "参数校验失败") || !strings.Contains(err.Error(), "-2") {
		t.Fatalf("error lost the server message: %v", err)
	}
}

func TestResolveSendsSignature(t *testing.T) {
	c, acts, forms := testClient(t, func(string, url.Values) string {
		return `{"code":0,"data":{"url":"https://cdn.example/a.flac"}}`
	})

	link, err := c.resolve("kuwo", track{ID: "42", Time: 1795630934, Sign: "abc"}, quality{Format: "flac", Bitrate: "2000"})
	if err != nil {
		t.Fatal(err)
	}
	if link != "https://cdn.example/a.flac" || (*acts)[0] != "getUrl" {
		t.Fatalf("got %q via %q", link, (*acts)[0])
	}
	form := (*forms)[0]
	for k, want := range map[string]string{
		"songid": "42", "format": "flac", "bitrate": "2000",
		"time": "1795630934", "sign": "abc", "platform": "kuwo",
	} {
		if form.Get(k) != want {
			t.Fatalf("form %s = %q, want %q", k, form.Get(k), want)
		}
	}
}

func TestParseTarget(t *testing.T) {
	cases := []struct {
		arg      string
		platform string
		kind     string
		id       string
	}{
		{"2407050", "kuwo", "song", "2407050"},
		{"https://www.kuwo.cn/play_detail/2407050", "kuwo", "song", "2407050"},
		{"https://www.kuwo.cn/yinyue/123", "kuwo", "song", "123"},
		{"https://www.kuwo.cn/album_detail/215821", "kuwo", "album", "215821"},
		{"https://www.kuwo.cn/playlist_detail/3823890", "kuwo", "playlist", "3823890"},
		{"https://www.kuwo.cn/playlist?albumid=77", "kuwo", "album", "77"},
	}
	for _, tc := range cases {
		got, err := parseTarget(tc.arg, "kuwo", "song")
		if err != nil {
			t.Fatalf("%s: %v", tc.arg, err)
		}
		if got.platform != tc.platform || got.kind != tc.kind || got.id != tc.id {
			t.Fatalf("%s: got %#v", tc.arg, got)
		}
	}

	got, err := parseTarget("https://other-lv.kuwo.cn/tok/resource/F000.flac", "kuwo", "song")
	if err != nil {
		t.Fatal(err)
	}
	if got.kind != "direct" || got.url == "" {
		t.Fatalf("cdn url not direct: %#v", got)
	}

	if _, err := parseTarget("https://music.163.com/song?id=1", "kuwo", "song"); err == nil {
		t.Fatal("want netease to be rejected")
	}
	if _, err := parseTarget("not-a-url", "kuwo", "song"); err == nil {
		t.Fatal("want junk to be rejected")
	}
}
