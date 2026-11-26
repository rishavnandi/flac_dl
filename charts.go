package main

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

//go:embed charts/*.json
var bundledCharts embed.FS

type appleChartClient struct {
	base     string
	http     *http.Client
	cacheDir string
	mu       sync.Mutex
	cache    map[string]*appleChartCache
	fallback map[string]appleChartFeed
}

type appleChartCache struct {
	feed    appleChartFeed
	err     error
	checked time.Time
	pending chan struct{}
}

type appleChartItem struct {
	Name        string `json:"name"`
	Artist      string `json:"artistName"`
	ArtworkURL  string `json:"artworkUrl100"`
	ReleaseDate string `json:"releaseDate"`
}

type appleChartFeed struct {
	Title   string           `json:"title"`
	Country string           `json:"country"`
	Updated string           `json:"updated"`
	Results []appleChartItem `json:"results"`
}

type appleChartResponse struct {
	Feed appleChartFeed `json:"feed"`
}

func newAppleChartClient() *appleChartClient {
	dir := os.Getenv("CACHE_DIRECTORY")
	if dir == "" {
		if base, err := os.UserCacheDir(); err == nil {
			dir = filepath.Join(base, "flac_dl")
		}
	}
	if os.Getenv("VERCEL") == "1" {
		dir = ""
	}
	c := &appleChartClient{
		base:     "https://rss.applemarketingtools.com/api/v2/us/music/most-played/20",
		http:     &http.Client{Timeout: 12 * time.Second},
		cacheDir: dir,
		fallback: make(map[string]appleChartFeed),
	}
	for _, kind := range []string{"songs", "albums"} {
		data, err := bundledCharts.ReadFile("charts/" + kind + ".json")
		if err != nil {
			panic(err)
		}
		feed, err := decodeAppleChart(bytes.NewReader(data))
		if err != nil {
			panic(err)
		}
		c.fallback[kind] = feed
	}
	return c
}

// Web navigation serves the last good feed while one background request refreshes it.
func (c *appleChartClient) cachedFetchContext(ctx context.Context, kind string) (appleChartFeed, error) {
	if kind != "songs" && kind != "albums" {
		return appleChartFeed{}, fmt.Errorf("unsupported Apple chart %q", kind)
	}
	c.mu.Lock()
	if c.cache == nil {
		c.cache = make(map[string]*appleChartCache)
	}
	entry := c.cache[kind]
	if entry == nil {
		entry = &appleChartCache{feed: c.fallback[kind]}
		if c.cacheDir != "" {
			if f, err := os.Open(filepath.Join(c.cacheDir, kind+".json")); err == nil {
				if feed, err := decodeAppleChart(f); err == nil {
					entry.feed = feed
					if st, err := f.Stat(); err == nil {
						entry.checked = st.ModTime()
					}
				}
				f.Close()
			}
		}
		c.cache[kind] = entry
	}
	hasFeed := len(entry.feed.Results) > 0
	age := time.Since(entry.checked)
	refresh := age >= 15*time.Minute || (entry.err != nil && age >= 30*time.Second)
	if entry.pending == nil && (refresh || (entry.checked.IsZero() && !hasFeed)) {
		entry.pending = make(chan struct{})
		go c.refreshCache(kind, entry)
	}
	if hasFeed || entry.pending == nil {
		feed, err := entry.feed, entry.err
		c.mu.Unlock()
		if hasFeed {
			err = nil
		}
		return feed, err
	}
	pending := entry.pending
	c.mu.Unlock()
	select {
	case <-ctx.Done():
		return appleChartFeed{}, ctx.Err()
	case <-pending:
		c.mu.Lock()
		feed, err := entry.feed, entry.err
		c.mu.Unlock()
		return feed, err
	}
}

func (c *appleChartClient) refreshCache(kind string, entry *appleChartCache) {
	feed, err := c.fetch(kind)
	if err == nil && c.cacheDir != "" {
		if saveErr := c.saveCache(kind, feed); saveErr != nil {
			log.Printf("save Apple chart cache: %v", saveErr)
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err == nil {
		entry.feed = feed
	}
	entry.err, entry.checked = err, time.Now()
	close(entry.pending)
	entry.pending = nil
}

func (c *appleChartClient) saveCache(kind string, feed appleChartFeed) error {
	if err := os.MkdirAll(c.cacheDir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(c.cacheDir, kind+"-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	err = json.NewEncoder(f).Encode(appleChartResponse{Feed: feed})
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(c.cacheDir, kind+".json"))
}

func (c *appleChartClient) fetch(kind string) (appleChartFeed, error) {
	return c.fetchContext(context.Background(), kind)
}

func (c *appleChartClient) fetchContext(ctx context.Context, kind string) (appleChartFeed, error) {
	if kind != "songs" && kind != "albums" {
		return appleChartFeed{}, fmt.Errorf("unsupported Apple chart %q", kind)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.base, "/")+"/"+kind+".json", nil)
	if err != nil {
		return appleChartFeed{}, err
	}
	req.Header.Set("User-Agent", "flac_dl/0.1.0")
	resp, err := c.http.Do(req)
	if err != nil {
		return appleChartFeed{}, fmt.Errorf("Apple %s chart: %w", kind, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return appleChartFeed{}, fmt.Errorf("Apple %s chart: http %s", kind, resp.Status)
	}
	feed, err := decodeAppleChart(resp.Body)
	if err != nil {
		return appleChartFeed{}, fmt.Errorf("Apple %s chart: %w", kind, err)
	}
	return feed, nil
}

func decodeAppleChart(r io.Reader) (appleChartFeed, error) {
	body, err := io.ReadAll(io.LimitReader(r, (2<<20)+1))
	if err != nil {
		return appleChartFeed{}, err
	}
	if len(body) > 2<<20 {
		return appleChartFeed{}, fmt.Errorf("feed exceeds 2 MB")
	}
	var result appleChartResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return appleChartFeed{}, err
	}
	if len(result.Feed.Results) == 0 {
		return appleChartFeed{}, fmt.Errorf("feed has no chart entries")
	}
	return result.Feed, nil
}
