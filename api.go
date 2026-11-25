package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	apiEndpoint = "https://flac.music.hi.cn/ajax.php"
	pageSize    = 20
	maxPages    = 50
)

// loose is a string that accepts a JSON string or number. The API returns ids,
// durations and bitrates in both forms.
type loose string

func (l *loose) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	switch {
	case s == "null":
		*l = ""
	case strings.HasPrefix(s, `"`):
		var v string
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		*l = loose(v)
	default:
		*l = loose(s)
	}
	return nil
}

type quality struct {
	Level   string `json:"level"`
	Bitrate loose  `json:"bitrate"`
	Format  string `json:"format"`
	Size    string `json:"size"`
}

type track struct {
	ID        loose     `json:"id"`
	Name      string    `json:"name"`
	Artist    string    `json:"artist"`
	AlbumName string    `json:"album_name"`
	AlbumID   loose     `json:"album_id"`
	Duration  loose     `json:"duration"`
	Minfo     []quality `json:"minfo"`
	Link      string    `json:"link"`
	Time      int64     `json:"time"`
	Sign      string    `json:"sign"`
}

type collectionInfo struct {
	ID     loose  `json:"id"`
	Name   string `json:"name"`
	Artist string `json:"artist"`
	Link   string `json:"link"`
}

type collection struct {
	Info  *collectionInfo `json:"info"`
	List  []track         `json:"list"`
	Total int             `json:"total"`
}

type client struct {
	base string
	http *http.Client
}

func newClient() *client {
	return &client{base: apiEndpoint, http: &http.Client{Timeout: 20 * time.Second}}
}

func (c *client) post(act string, params url.Values) (json.RawMessage, error) {
	req, err := http.NewRequest(http.MethodPost, c.base+"?act="+url.QueryEscape(act), strings.NewReader(params.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=UTF-8")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", act, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", act, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: http %s", act, resp.Status)
	}

	var env struct {
		Code int             `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("%s: unexpected response %.120q", act, body)
	}
	if env.Code != 0 {
		return nil, fmt.Errorf("%s: %s (code %d)", act, env.Msg, env.Code)
	}
	return env.Data, nil
}

func (c *client) search(platform, keyword string, limit int) ([]track, int, error) {
	var out []track
	total := 0
	for page := 1; page <= maxPages; page++ {
		data, err := c.post("search", url.Values{
			"platform": {platform},
			"keyword":  {keyword},
			"page":     {strconv.Itoa(page)},
			"size":     {strconv.Itoa(pageSize)},
		})
		if err != nil {
			return nil, 0, err
		}
		var got struct {
			List  []track `json:"list"`
			Total int     `json:"total"`
		}
		if err := json.Unmarshal(data, &got); err != nil {
			return nil, 0, fmt.Errorf("search: %w", err)
		}
		total = got.Total
		out = append(out, got.List...)
		if len(got.List) < pageSize || (total > 0 && len(out) >= total) || len(out) >= limit {
			break
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, total, nil
}

// collection fetches an album, playlist or song. Albums come back whole
// regardless of page size, playlists page, so loop until a short page.
func (c *client) collection(kind, platform, id string) (collection, error) {
	var out collection
	for page := 1; page <= maxPages; page++ {
		data, err := c.post(kind, url.Values{
			"platform": {platform},
			"id":       {id},
			"page":     {strconv.Itoa(page)},
			"size":     {strconv.Itoa(pageSize)},
		})
		if err != nil {
			return collection{}, err
		}
		var got collection
		if err := json.Unmarshal(data, &got); err != nil {
			return collection{}, fmt.Errorf("%s: %w", kind, err)
		}
		if got.Info != nil {
			out.Info = got.Info
		}
		out.Total = got.Total
		out.List = append(out.List, got.List...)
		if len(got.List) < pageSize || (got.Total > 0 && len(out.List) >= got.Total) {
			break
		}
	}
	return out, nil
}

func (c *client) resolve(platform string, t track, q quality) (string, error) {
	data, err := c.post("getUrl", url.Values{
		"platform": {platform},
		"songid":   {string(t.ID)},
		"format":   {q.Format},
		"bitrate":  {string(q.Bitrate)},
		"time":     {strconv.FormatInt(t.Time, 10)},
		"sign":     {t.Sign},
	})
	if err != nil {
		return "", err
	}
	var got struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		return "", fmt.Errorf("getUrl: %w", err)
	}
	if got.URL == "" {
		return "", errors.New("getUrl: empty url")
	}
	return got.URL, nil
}

func (c *client) lyric(platform string, t track) (string, error) {
	data, err := c.post("getLyric", url.Values{
		"platform": {platform},
		"songid":   {string(t.ID)},
		"time":     {strconv.FormatInt(t.Time, 10)},
		"sign":     {t.Sign},
	})
	if err != nil {
		return "", err
	}
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return "", fmt.Errorf("getLyric: %w", err)
	}
	return text, nil
}

type target struct {
	platform string
	kind     string // song, album, playlist or direct
	id       string
	url      string
}

var (
	digits    = regexp.MustCompile(`^\d+$`)
	audioFile = regexp.MustCompile(`(?i)\.(flac|mp3|m4a|aac|wav)$`)
	kuwoKinds = []struct {
		kind string
		re   *regexp.Regexp
	}{
		{"album", regexp.MustCompile(`/album_detail/(\d+)`)},
		{"playlist", regexp.MustCompile(`/playlist_detail/(\d+)`)},
		{"song", regexp.MustCompile(`/play_detail/(\d+)`)},
		{"song", regexp.MustCompile(`/yinyue/(\d+)`)},
	}
)

// parseTarget turns an id or page link into something the API understands.
// A bare number takes defaultKind, since only the caller knows whether it asked
// for a song or an album.
func parseTarget(arg, platform, defaultKind string) (target, error) {
	arg = strings.TrimSpace(arg)
	if digits.MatchString(arg) {
		return target{platform: platform, kind: defaultKind, id: arg}, nil
	}
	if !strings.HasPrefix(arg, "http") {
		return target{}, fmt.Errorf("want an id or url, got %q", arg)
	}
	u, err := url.Parse(arg)
	if err != nil {
		return target{}, err
	}

	host := u.Hostname()
	switch {
	case isHost(host, "kuwo.cn"):
		if id := u.Query().Get("albumid"); digits.MatchString(id) {
			return target{platform: "kuwo", kind: "album", id: id}, nil
		}
		for _, k := range kuwoKinds {
			if m := k.re.FindStringSubmatch(u.Path); m != nil {
				return target{platform: "kuwo", kind: k.kind, id: m[1]}, nil
			}
		}
		// A CDN link has a token in the path and no page pattern.
		if audioFile.MatchString(u.Path) {
			return target{kind: "direct", url: arg}, nil
		}
		return target{}, fmt.Errorf("no album, playlist or song id in %s", arg)
	case isHost(host, "163.com"):
		return target{}, errors.New("netease links are unsupported: the site's netease backend is broken, use a kuwo link or a plain search")
	}
	return target{kind: "direct", url: arg}, nil
}

func isHost(host, domain string) bool {
	return host == domain || strings.HasSuffix(host, "."+domain)
}
