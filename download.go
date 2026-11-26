package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

const maxNameBytes = 180
const downloadDir = "downloads"

var unsafeName = regexp.MustCompile(`[/\\:*?"<>|\x00-\x1f\x7f]`)

type job struct {
	platform string
	track    track
	quality  quality
	dir      string
	lrc      bool
	force    bool
}

type downloader struct {
	client *client
	http   *http.Client
}

func newDownloader(c *client) *downloader {
	return &downloader{client: c, http: &http.Client{Timeout: 15 * time.Minute}}
}

// fetch resolves a fresh CDN link and writes one track, then its lyrics.
// Links are tokens that expire, so a failed transfer is retried from a new
// resolve rather than resumed.
func (d *downloader) fetch(j job) (path string, skipped bool, err error) {
	return d.fetchWith(j, nil)
}

type progressFunc func(done, total int64)

func (d *downloader) fetchWith(j job, progress progressFunc) (path string, skipped bool, err error) {
	path = filepath.Join(j.dir, fileName(j.track, extOf(j.quality)))
	if !j.force {
		if st, statErr := os.Stat(path); statErr == nil && st.Size() > 0 {
			return path, true, nil
		}
	}
	if err := os.MkdirAll(j.dir, 0o755); err != nil {
		return "", false, err
	}

	var last error
	for attempt := 0; attempt < 2; attempt++ {
		link, resolveErr := d.client.resolve(j.platform, j.track, j.quality)
		if resolveErr != nil {
			return "", false, resolveErr
		}
		if last = d.getWith(link, path, progress); last == nil {
			break
		}
	}
	if last != nil {
		return "", false, last
	}

	if j.lrc {
		if err := d.writeLyric(j, path); err != nil {
			return path, false, fmt.Errorf("lyric: %w", err)
		}
	}
	return path, false, nil
}

func (d *downloader) get(link, path string) error {
	return d.getWith(link, path, nil)
}

func (d *downloader) getWith(link, path string, progress progressFunc) error {
	req, err := http.NewRequest(http.MethodGet, link, nil)
	if err != nil {
		return err
	}
	resp, err := d.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("http %s", resp.Status)
	}

	part := path + ".part"
	f, err := os.Create(part)
	if err != nil {
		return err
	}
	var src io.Reader = resp.Body
	if progress != nil {
		src = &progressReader{r: resp.Body, total: resp.ContentLength, cb: progress}
	}
	n, err := io.Copy(f, src)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(part)
		return err
	}
	if resp.ContentLength > 0 && n != resp.ContentLength {
		os.Remove(part)
		return fmt.Errorf("truncated: %d of %d bytes", n, resp.ContentLength)
	}
	if progress != nil {
		progress(n, resp.ContentLength)
	}
	return os.Rename(part, path)
}

type progressReader struct {
	r     io.Reader
	total int64
	read  int64
	last  time.Time
	cb    progressFunc
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.read += int64(n)
	if now := time.Now(); now.Sub(p.last) >= 100*time.Millisecond || err != nil {
		p.last = now
		p.cb(p.read, p.total)
	}
	return n, err
}

func (d *downloader) writeLyric(j job, audioPath string) error {
	lrc, err := d.client.lyric(j.platform, j.track)
	if err != nil {
		return err
	}
	if strings.TrimSpace(lrc) == "" {
		return nil
	}
	path := strings.TrimSuffix(audioPath, filepath.Ext(audioPath)) + ".lrc"
	return os.WriteFile(path, []byte(lrc), 0o644)
}

func fileName(t track, ext string) string {
	name := strings.TrimSpace(t.Name)
	if t.Artist != "" {
		name = t.Artist + " - " + name
	}
	name = unsafeName.ReplaceAllString(name, "_")
	name = strings.Join(strings.Fields(name), " ")
	name = strings.Trim(name, " .")
	if name == "" {
		name = "track " + string(t.ID)
	}
	return truncateBytes(name, maxNameBytes) + ext
}

// truncateBytes cuts on a rune boundary so a CJK title never ends mid character.
func truncateBytes(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := s[:max]
	for len(cut) > 0 {
		r, size := utf8.DecodeLastRuneInString(cut)
		if r != utf8.RuneError || size > 1 {
			break
		}
		cut = cut[:len(cut)-1]
	}
	return strings.TrimSpace(cut)
}

func extOf(q quality) string {
	if q.Format == "flac" {
		return ".flac"
	}
	return ".mp3"
}

func label(t track) string {
	if t.Artist != "" {
		return t.Artist + " - " + t.Name
	}
	return t.Name
}

func clock(d loose) string {
	secs, err := strconv.Atoi(string(d))
	if err != nil || secs <= 0 {
		return "-:--"
	}
	return fmt.Sprintf("%d:%02d", secs/60, secs%60)
}

func pickQuality(t track, want string) (quality, error) {
	if len(t.Minfo) == 0 {
		return quality{}, fmt.Errorf("no formats listed")
	}
	switch strings.ToLower(strings.TrimSpace(want)) {
	case "", "auto":
		for _, q := range t.Minfo {
			if q.Format == "flac" {
				return q, nil
			}
		}
		for _, q := range t.Minfo {
			if string(q.Bitrate) == "320" {
				return q, nil
			}
		}
		return t.Minfo[len(t.Minfo)-1], nil
	case "flac":
		for _, q := range t.Minfo {
			if q.Format == "flac" {
				return q, nil
			}
		}
	case "320", "128":
		for _, q := range t.Minfo {
			if q.Format == "mp3" && string(q.Bitrate) == want {
				return q, nil
			}
		}
	}
	return quality{}, fmt.Errorf("no %s version, available: %s", want, formatsOf(t))
}

func formatsOf(t track) string {
	parts := make([]string, 0, len(t.Minfo))
	for _, q := range t.Minfo {
		parts = append(parts, strings.ToUpper(q.Format)+"/"+string(q.Bitrate))
	}
	return strings.Join(parts, ", ")
}

func runJobs(c *client, jobs []job, workers int) error {
	if len(jobs) == 0 {
		return nil
	}
	if workers < 1 {
		workers = 1
	}
	if workers > len(jobs) {
		workers = len(jobs)
	}

	d := newDownloader(c)
	queue := make(chan job)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var done atomic.Int64
	failed := 0

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range queue {
				start := time.Now()
				path, skipped, err := d.fetch(j)
				n := done.Add(1)
				mu.Lock()
				switch {
				case err != nil:
					failed++
					fmt.Fprintf(os.Stderr, "%s x %s: %v\n", counter(n, len(jobs)), label(j.track), err)
				case skipped:
					fmt.Fprintf(os.Stderr, "%s = %s (exists)\n", counter(n, len(jobs)), filepath.Base(path))
				default:
					fmt.Fprintf(os.Stderr, "%s + %s (%.1fs)\n", counter(n, len(jobs)), filepath.Base(path), time.Since(start).Seconds())
				}
				mu.Unlock()
			}
		}()
	}
	for _, j := range jobs {
		queue <- j
	}
	close(queue)
	wg.Wait()

	if failed > 0 {
		return fmt.Errorf("%d of %d downloads failed", failed, len(jobs))
	}
	return nil
}

func counter(n int64, total int) string {
	if total == 1 {
		return ""
	}
	return fmt.Sprintf("[%d/%d]", n, total)
}
