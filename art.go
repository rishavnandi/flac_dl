package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
)

var (
	artHTTP           = &http.Client{Timeout: 6 * time.Second}
	itunesSearchURL   = "https://itunes.apple.com/search"
	itunesArtworkSize = regexp.MustCompile(`/[0-9]+x[0-9]+bb`)
)

// coverURL preserves Kuwo's URL. Its 120px covers are enough for the TUI.
func coverURL(pic string) string {
	pic = strings.TrimSpace(pic)
	if pic == "" {
		return ""
	}
	return strings.Replace(pic, "http://", "https://", 1)
}

func artworkKey(t track) string {
	if pic := coverURL(t.PicURL); pic != "" {
		return pic
	}
	if t.Artist == "" || t.AlbumName == "" {
		return ""
	}
	return "itunes:" + normalizeArtworkText(t.Artist) + ":" + normalizeArtworkText(t.AlbumName)
}

func fetchCover(pic, artist, album string, cols int) (string, error) {
	var kuwoErr error
	if pic = coverURL(pic); pic != "" {
		if art, err := fetchArt(pic, cols); err == nil {
			return art, nil
		} else {
			kuwoErr = err
		}
	}

	fallback, err := itunesArtworkURL(artist, album)
	if err != nil {
		if kuwoErr != nil {
			return "", fmt.Errorf("Kuwo: %v; iTunes fallback: %w", kuwoErr, err)
		}
		return "", err
	}
	art, err := fetchArt(fallback, cols)
	if err != nil && kuwoErr != nil {
		return "", fmt.Errorf("Kuwo: %v; iTunes fallback: %w", kuwoErr, err)
	}
	return art, err
}

type itunesResponse struct {
	Results []struct {
		ArtistName    string `json:"artistName"`
		Collection    string `json:"collectionName"`
		ArtworkURL100 string `json:"artworkUrl100"`
	} `json:"results"`
}

func itunesArtworkURL(artist, album string) (string, error) {
	if strings.TrimSpace(artist) == "" || strings.TrimSpace(album) == "" {
		return "", errors.New("no artist/album for artwork lookup")
	}
	query := url.Values{
		"term":   {artist + " " + album},
		"entity": {"album"},
		"limit":  {"5"},
	}
	req, err := http.NewRequest(http.MethodGet, itunesSearchURL+"?"+query.Encode(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "flac_dl/0.1.0")
	resp, err := artHTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("iTunes search: http %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, (1<<20)+1))
	if err != nil {
		return "", err
	}
	if len(body) > 1<<20 {
		return "", errors.New("iTunes response exceeds 1 MB")
	}
	var result itunesResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("iTunes response: %w", err)
	}

	wantArtist := normalizeArtworkText(artist)
	wantAlbum := normalizeArtworkText(album)
	for _, item := range result.Results {
		if normalizeArtworkText(item.ArtistName) == wantArtist &&
			normalizeArtworkText(item.Collection) == wantAlbum &&
			item.ArtworkURL100 != "" {
			return itunesArtworkSize.ReplaceAllString(item.ArtworkURL100, "/600x600bb"), nil
		}
	}
	return "", errors.New("no exact artist and album match in iTunes")
}

func normalizeArtworkText(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func fetchArt(url string, cols int) (string, error) {
	if url == "" {
		return "", errors.New("no cover url")
	}
	resp, err := artHTTP.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("cover: http %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20+1))
	if err != nil {
		return "", err
	}
	if len(data) > 8<<20 {
		return "", errors.New("cover image exceeds 8 MB")
	}
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	if config.Width < 1 || config.Height < 1 || config.Width > 4096 || config.Height > 4096 {
		return "", errors.New("cover image dimensions exceed 4096×4096")
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	return renderArt(img, cols), nil
}

// renderArt draws a square cover with half blocks: each cell holds two vertical
// pixels, so cols columns need cols/2 rows. Every source pixel is box-averaged
// into its cell, which keeps downscaled art from looking noisy.
func renderArt(img image.Image, cols int) string {
	if cols < 2 {
		return ""
	}
	rows := (cols + 1) / 2
	b := img.Bounds()
	px := make([]color.NRGBA, cols*rows*2)

	for y := 0; y < rows*2; y++ {
		y0 := b.Min.Y + y*b.Dy()/(rows*2)
		y1 := b.Min.Y + (y+1)*b.Dy()/(rows*2)
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for x := 0; x < cols; x++ {
			x0 := b.Min.X + x*b.Dx()/cols
			x1 := b.Min.X + (x+1)*b.Dx()/cols
			if x1 <= x0 {
				x1 = x0 + 1
			}
			var r, g, bl, n uint64
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					c := color.NRGBAModel.Convert(img.At(sx, sy)).(color.NRGBA)
					r += uint64(c.R)
					g += uint64(c.G)
					bl += uint64(c.B)
					n++
				}
			}
			px[y*cols+x] = color.NRGBA{uint8(r / n), uint8(g / n), uint8(bl / n), 255}
		}
	}

	var sb strings.Builder
	sb.Grow(cols * rows * 24)
	for y := 0; y < rows; y++ {
		for x := 0; x < cols; x++ {
			top := px[(2*y)*cols+x]
			bottom := px[(2*y+1)*cols+x]
			fmt.Fprintf(&sb, "\x1b[38;2;%d;%d;%dm\x1b[48;2;%d;%d;%dm▀",
				top.R, top.G, top.B, bottom.R, bottom.G, bottom.B)
		}
		sb.WriteString("\x1b[0m")
		if y < rows-1 {
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}
