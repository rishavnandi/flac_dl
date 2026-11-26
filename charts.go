package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type appleChartClient struct {
	base string
	http *http.Client
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
	return &appleChartClient{
		base: "https://rss.applemarketingtools.com/api/v2/us/music/most-played/20",
		http: &http.Client{Timeout: 12 * time.Second},
	}
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
	body, err := io.ReadAll(io.LimitReader(resp.Body, (2<<20)+1))
	if err != nil {
		return appleChartFeed{}, fmt.Errorf("Apple %s chart: %w", kind, err)
	}
	if len(body) > 2<<20 {
		return appleChartFeed{}, fmt.Errorf("Apple %s chart exceeds 2 MB", kind)
	}
	var result appleChartResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return appleChartFeed{}, fmt.Errorf("Apple %s chart response: %w", kind, err)
	}
	return result.Feed, nil
}
