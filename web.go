package main

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

//go:embed web/templates/*.html web/static/*
var webFiles embed.FS

const webDefaultAddr = "127.0.0.1:8765"

type webApp struct {
	client      *client
	charts      *appleChartClient
	views       *template.Template
	downloads   *webDownloads
	publicHosts map[string]bool
}

type webPage struct {
	Hosted bool
	Title  string
	Active string
	Query  string
	View   template.HTML
}

type webView struct {
	Hosted      bool
	Title       string
	Eyebrow     string
	Description string
	Query       string
	ChartKind   string
	Charts      []webChartItem
	Hero        webChartItem
	Tracks      []webTrack
	Album       webAlbum
	Page        int
	HasMore     bool
	Error       string
}

type webChartItem struct {
	Rank       int
	Name       string
	Artist     string
	Artwork    string
	ArtworkAlt string
	SearchURL  string
}

type webTrack struct {
	Hosted          bool
	ID              string
	Name            string
	Artist          string
	AlbumName       string
	Duration        string
	Quality         string
	Artwork         string
	ArtworkAlt      string
	ArtworkFallback string
	AlbumURL        string
}

type webAlbum struct {
	ID              string
	Name            string
	Artist          string
	Artwork         string
	ArtworkFallback string
}

type webDownloadView struct {
	ID              string
	Label           string
	State           string
	Total           int
	Finished        int
	Active          int
	Skipped         int
	Failed          int
	Error           string
	Bytes           int64
	TotalBytes      int64
	ProgressPercent int
}

type webDownloadRecord struct {
	webDownloadView
	updated  time.Time
	progress map[int]webByteProgress
}

type webByteProgress struct {
	done  int64
	total int64
}

type webDownloads struct {
	mu      sync.RWMutex
	next    atomic.Uint64
	records map[string]*webDownloadRecord
}

func newWebApp(c *client, charts *appleChartClient) (*webApp, error) {
	views, err := template.New("site").Funcs(template.FuncMap{
		"add": func(a, b int) int { return a + b },
	}).ParseFS(webFiles, "web/templates/*.html")
	if err != nil {
		return nil, err
	}
	return &webApp{
		client:    c,
		charts:    charts,
		views:     views,
		downloads: &webDownloads{records: make(map[string]*webDownloadRecord)},
	}, nil
}

func (a *webApp) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", a.home)
	mux.HandleFunc("GET /charts/{kind}", a.chart)
	mux.HandleFunc("GET /search", a.search)
	mux.HandleFunc("GET /album", a.album)
	mux.HandleFunc("GET /stream", a.stream)
	mux.HandleFunc("GET /artwork", a.artwork)
	mux.HandleFunc("POST /download", a.download)
	mux.HandleFunc("GET /download-status", a.downloadStatus)
	static, err := fs.Sub(webFiles, "web/static")
	if err != nil {
		panic(err)
	}
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(static))))
	return securityHeaders(mux, a.publicHosts)
}

func newWebHandler() (http.Handler, error) {
	app, err := newWebApp(newClient(), newAppleChartClient())
	if err != nil {
		return nil, err
	}
	return app.handler(), nil
}

func securityHeaders(next http.Handler, publicHosts map[string]bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' https: data:; media-src 'self' https: blob:; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self' https://lrclib.net")
		allowed := isLoopbackHost(r.Host)
		if publicHosts != nil {
			allowed = publicHosts[strings.ToLower(r.Host)]
		}
		if !allowed {
			http.Error(w, "unrecognized host", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isLoopbackHost(hostPort string) bool {
	host, _, err := net.SplitHostPort(hostPort)
	if err != nil {
		host = strings.Trim(hostPort, "[]")
	}
	if strings.EqualFold(strings.TrimSuffix(host, "."), "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func isSameOrigin(r *http.Request) bool {
	origin, err := url.Parse(r.Header.Get("Origin"))
	return err == nil && origin.Scheme == "http" && strings.EqualFold(origin.Host, r.Host)
}

func (a *webApp) home(w http.ResponseWriter, r *http.Request) {
	a.renderChart(w, r, "songs")
}

func (a *webApp) chart(w http.ResponseWriter, r *http.Request) {
	a.renderChart(w, r, r.PathValue("kind"))
}

func (a *webApp) renderChart(w http.ResponseWriter, r *http.Request, kind string) {
	if kind != "songs" && kind != "albums" {
		http.NotFound(w, r)
		return
	}
	feed, err := a.charts.fetchContext(r.Context(), kind)
	if err != nil {
		a.renderError(w, r, "Chart unavailable", err)
		return
	}
	items := make([]webChartItem, 0, len(feed.Results))
	for i, item := range feed.Results {
		items = append(items, chartItem(i+1, item))
	}
	view := webView{
		Title:       "US Top " + kind,
		Eyebrow:     "APPLE MUSIC · UNITED STATES",
		Description: "The latest chart, ready to explore on Kuwo.",
		ChartKind:   kind,
		Charts:      items,
	}
	if len(items) > 0 {
		view.Hero = items[0]
	}
	a.render(w, r, webPage{Title: view.Title, Active: "charts"}, "charts", view, http.StatusOK)
}

func chartItem(rank int, item appleChartItem) webChartItem {
	query := strings.TrimSpace(item.Name + " " + item.Artist)
	return webChartItem{
		Rank:       rank,
		Name:       item.Name,
		Artist:     item.Artist,
		Artwork:    webArtworkURL(item.ArtworkURL),
		ArtworkAlt: item.Name + " cover",
		SearchURL:  "/search?" + url.Values{"q": {query}}.Encode(),
	}
}

func (a *webApp) search(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		a.renderError(w, r, "Search for a song or artist", errors.New("enter a search term to browse Kuwo"))
		return
	}
	page := 1
	if n, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && n > 1 {
		page = n
	}
	if page > 10 {
		page = 10
	}
	var tracks []track
	total := 0
	for p := 1; p <= page; p++ {
		list, gotTotal, err := a.client.searchPageContext(r.Context(), "kuwo", query, p)
		if err != nil {
			a.renderError(w, r, "Search unavailable", err)
			return
		}
		tracks = append(tracks, list...)
		total = gotTotal
		if len(list) < pageSize || (total > 0 && len(tracks) >= total) {
			break
		}
	}
	view := webView{
		Title:       "Search",
		Eyebrow:     "KUWO CATALOG",
		Description: fmt.Sprintf("%d results for “%s”", total, query),
		Query:       query,
		Tracks:      webTracks(tracks),
		Page:        page,
		HasMore:     len(tracks) < total && page < 10,
	}
	a.render(w, r, webPage{Title: "Search · " + query, Active: "search", Query: query}, "search", view, http.StatusOK)
}

func (a *webApp) album(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if !digits.MatchString(id) {
		http.Error(w, "invalid album id", http.StatusBadRequest)
		return
	}
	col, err := a.client.collectionContext(r.Context(), "album", "kuwo", id)
	if (err != nil || len(col.List) == 0) && digits.MatchString(id) {
		if fallback, fallbackErr := a.client.collectionContext(r.Context(), "playlist", "kuwo", id); fallbackErr == nil && len(fallback.List) > 0 {
			col, err = fallback, nil
		}
	}
	if err != nil {
		a.renderError(w, r, "Album unavailable", err)
		return
	}
	if len(col.List) == 0 {
		a.renderError(w, r, "No tracks found", fmt.Errorf("the Kuwo album %s has no tracks", id))
		return
	}
	name, artist, art := "Album "+id, "", ""
	if col.Info != nil {
		if col.Info.Name != "" {
			name = col.Info.Name
		}
		artist = col.Info.Artist
		art = webArtworkURL(col.Info.PicURL)
	}
	tracks := webTracks(col.List)
	if art == "" && len(tracks) > 0 {
		art = tracks[0].Artwork
	}
	view := webView{
		Title:       name,
		Eyebrow:     "ALBUM",
		Description: fmt.Sprintf("%s · %d tracks", artist, len(tracks)),
		Album: webAlbum{
			ID: id, Name: name, Artist: artist, Artwork: art,
			ArtworkFallback: "/artwork?" + url.Values{"artist": {artist}, "album": {name}}.Encode(),
		},
		Tracks: tracks,
	}
	a.render(w, r, webPage{Title: name, Active: "albums"}, "album", view, http.StatusOK)
}

func (a *webApp) stream(w http.ResponseWriter, r *http.Request) {
	save := r.URL.Query().Get("download") == "1"
	if save && a.publicHosts == nil {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if !digits.MatchString(id) {
		http.Error(w, "invalid track id", http.StatusBadRequest)
		return
	}
	col, err := a.client.collectionContext(r.Context(), "song", "kuwo", id)
	if err != nil {
		http.Error(w, "could not load this track", http.StatusBadGateway)
		return
	}
	if len(col.List) == 0 {
		http.Error(w, "track not found", http.StatusNotFound)
		return
	}
	q, err := pickQuality(col.List[0], r.URL.Query().Get("q"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	link, err := a.client.resolveContext(r.Context(), "kuwo", col.List[0], q)
	if err != nil {
		http.Error(w, "could not resolve this audio stream", http.StatusBadGateway)
		return
	}
	u, err := url.Parse(link)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		http.Error(w, "invalid audio stream URL", http.StatusBadGateway)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if save {
		a.saveToBrowser(w, r, col.List[0], q, u)
		return
	}
	http.Redirect(w, r, u.String(), http.StatusFound)
}

func (a *webApp) saveToBrowser(w http.ResponseWriter, r *http.Request, t track, q quality, link *url.URL) {
	if !isHost(link.Hostname(), "kuwo.cn") || link.Port() != "" || link.User != nil {
		http.Error(w, "unrecognized audio source", http.StatusBadGateway)
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, link.String(), nil)
	if err != nil {
		http.Error(w, "invalid download URL", http.StatusBadGateway)
		return
	}
	c := *a.client.http
	c.Timeout = 4 * time.Minute
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 || req.URL.Scheme != "https" || !isHost(req.URL.Hostname(), "kuwo.cn") || req.URL.Port() != "" || req.URL.User != nil {
			return errors.New("unrecognized audio redirect")
		}
		return nil
	}
	resp, err := c.Do(req)
	if err != nil {
		log.Printf("browser download: %v", err)
		http.Error(w, "could not fetch this track", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		http.Error(w, "audio source unavailable", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{
		"filename": fileName(t, extOf(q)),
	}))
	if resp.ContentLength >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(resp.ContentLength, 10))
	}
	w.WriteHeader(http.StatusOK)
	if err := http.NewResponseController(w).Flush(); err != nil {
		log.Printf("flush browser download: %v", err)
		return
	}
	if _, err := io.Copy(w, resp.Body); err != nil {
		log.Printf("stream browser download: %v", err)
	}
}

func (a *webApp) artwork(w http.ResponseWriter, r *http.Request) {
	art, err := itunesArtworkURL(r.URL.Query().Get("artist"), r.URL.Query().Get("album"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, art, http.StatusFound)
}

func (a *webApp) download(w http.ResponseWriter, r *http.Request) {
	if a.publicHosts != nil {
		http.Error(w, "hosted downloads save directly to your browser", http.StatusMethodNotAllowed)
		return
	}
	if !isSameOrigin(r) {
		http.Error(w, "same-origin requests only", http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		status := http.StatusBadRequest
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			status = http.StatusRequestEntityTooLarge
		}
		http.Error(w, "invalid download request", status)
		return
	}
	id := strings.TrimSpace(r.Form.Get("track_id"))
	if !digits.MatchString(id) {
		http.Error(w, "invalid track or album id", http.StatusBadRequest)
		return
	}
	qualityChoice := r.Form.Get("quality")
	lyrics := r.Form.Get("lyrics") == "1"
	var tracks []track
	labelText := ""
	if albumID := strings.TrimSpace(r.Form.Get("album")); albumID != "" {
		if albumID != id {
			http.Error(w, "invalid album id", http.StatusBadRequest)
			return
		}
		col, err := a.client.collectionContext(r.Context(), "album", "kuwo", id)
		if err != nil || len(col.List) == 0 {
			col, err = a.client.collectionContext(r.Context(), "playlist", "kuwo", id)
		}
		if err != nil {
			a.renderError(w, r, "Download could not start", err)
			return
		}
		tracks = col.List
		labelText = "Album " + id
		if col.Info != nil && col.Info.Name != "" {
			labelText = col.Info.Name
		}
	} else {
		col, err := a.client.collectionContext(r.Context(), "song", "kuwo", id)
		if err != nil {
			a.renderError(w, r, "Download could not start", err)
			return
		}
		if len(col.List) == 0 {
			a.renderError(w, r, "Download could not start", fmt.Errorf("track %s was not found", id))
			return
		}
		tracks = col.List[:1]
		labelText = label(tracks[0])
	}
	jobs := make([]job, 0, len(tracks))
	skipped := 0
	for _, t := range tracks {
		q, err := pickQuality(t, qualityChoice)
		if err != nil {
			skipped++
			continue
		}
		jobs = append(jobs, job{platform: "kuwo", track: t, quality: q, dir: downloadDir, lrc: lyrics})
	}
	if len(jobs) == 0 {
		a.renderError(w, r, "Download could not start", fmt.Errorf("no tracks have the selected quality"))
		return
	}
	id = a.downloads.start(a.client, labelText, jobs, skipped)
	record, _ := a.downloads.get(id)
	a.renderDownload(w, *record)
}

func (a *webApp) downloadStatus(w http.ResponseWriter, r *http.Request) {
	if a.publicHosts != nil {
		http.NotFound(w, r)
		return
	}
	record, ok := a.downloads.get(r.URL.Query().Get("id"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	a.renderDownload(w, *record)
}

func (a *webApp) renderDownload(w http.ResponseWriter, record webDownloadRecord) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := a.views.ExecuteTemplate(w, "download-status", record.webDownloadView); err != nil {
		log.Printf("render download status: %v", err)
	}
}

func (a *webApp) renderError(w http.ResponseWriter, r *http.Request, title string, err error) {
	view := webView{Title: title, Eyebrow: "NOTICE", Error: err.Error()}
	a.render(w, r, webPage{Title: title}, "error", view, http.StatusOK)
}

func (a *webApp) render(w http.ResponseWriter, r *http.Request, page webPage, viewName string, view webView, status int) {
	page.Hosted = a.publicHosts != nil
	view.Hosted = page.Hosted
	for i := range view.Tracks {
		view.Tracks[i].Hosted = page.Hosted
	}
	var content bytes.Buffer
	if err := a.views.ExecuteTemplate(&content, viewName, view); err != nil {
		http.Error(w, "could not render page", http.StatusInternalServerError)
		log.Printf("render %s: %v", viewName, err)
		return
	}
	page.View = template.HTML(content.String())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Add("Vary", "HX-Request")
	w.WriteHeader(status)
	name := "layout"
	if r.Header.Get("HX-Request") == "true" && r.Header.Get("HX-History-Restore-Request") != "true" {
		name = "view-only"
	}
	if err := a.views.ExecuteTemplate(w, name, page); err != nil {
		log.Printf("render layout: %v", err)
	}
}

func webTracks(tracks []track) []webTrack {
	out := make([]webTrack, 0, len(tracks))
	for _, t := range tracks {
		q, _ := pickQuality(t, "auto")
		albumURL := ""
		if digits.MatchString(string(t.AlbumID)) {
			albumURL = "/album?id=" + url.QueryEscape(string(t.AlbumID))
		}
		art := webArtworkURL(t.PicURL)
		artFallback := ""
		if strings.TrimSpace(t.Artist) != "" && strings.TrimSpace(t.AlbumName) != "" {
			artFallback = "/artwork?" + url.Values{"artist": {t.Artist}, "album": {t.AlbumName}}.Encode()
		}
		out = append(out, webTrack{
			ID:              string(t.ID),
			Name:            t.Name,
			Artist:          t.Artist,
			AlbumName:       t.AlbumName,
			Duration:        clock(t.Duration),
			Quality:         strings.ToUpper(q.Format),
			Artwork:         art,
			ArtworkAlt:      t.AlbumName + " cover",
			ArtworkFallback: artFallback,
			AlbumURL:        albumURL,
		})
	}
	return out
}

func webArtworkURL(raw string) string {
	raw = coverURL(raw)
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return ""
	}
	return itunesArtworkSize.ReplaceAllString(u.String(), "/600x600bb")
}

func (d *webDownloads) start(c *client, labelText string, jobs []job, skipped int) string {
	id := "download-" + strconv.FormatUint(d.next.Add(1), 10)
	record := &webDownloadRecord{webDownloadView: webDownloadView{
		ID: id, Label: labelText, State: "active", Total: len(jobs) + skipped, Finished: skipped, Skipped: skipped,
	}, updated: time.Now(), progress: make(map[int]webByteProgress)}
	d.mu.Lock()
	for key, old := range d.records {
		if old.State != "active" && time.Since(old.updated) > 15*time.Minute {
			delete(d.records, key)
		}
	}
	d.records[id] = record
	d.mu.Unlock()

	go func() {
		sem := make(chan struct{}, min(4, len(jobs)))
		var wg sync.WaitGroup
		for i, j := range jobs {
			wg.Add(1)
			sem <- struct{}{}
			go func(index int, item job) {
				defer wg.Done()
				defer func() { <-sem }()
				d.update(id, func(r *webDownloadRecord) {
					r.Active++
					r.updated = time.Now()
				})
				_, wasSkipped, err := newDownloader(c).fetchWith(item, func(done, total int64) {
					d.update(id, func(r *webDownloadRecord) {
						if total < 0 {
							total = 0
						}
						r.progress[index] = webByteProgress{done: done, total: total}
						r.Bytes, r.TotalBytes = 0, 0
						for _, progress := range r.progress {
							r.Bytes += progress.done
							r.TotalBytes += progress.total
						}
						if r.TotalBytes > 0 {
							r.ProgressPercent = int(float64(r.Bytes) / float64(r.TotalBytes) * 100)
						}
						r.updated = time.Now()
					})
				})
				d.update(id, func(r *webDownloadRecord) {
					r.Active--
					r.Finished++
					if wasSkipped {
						r.Skipped++
					}
					if err != nil {
						r.Failed++
						if r.Error == "" {
							r.Error = err.Error()
						}
					}
					r.updated = time.Now()
				})
			}(i, j)
		}
		wg.Wait()
		d.update(id, func(r *webDownloadRecord) {
			if r.Failed > 0 {
				r.State = "error"
			} else {
				r.State = "done"
				r.ProgressPercent = 100
			}
			r.updated = time.Now()
		})
	}()
	return id
}

func (d *webDownloads) update(id string, fn func(*webDownloadRecord)) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if record := d.records[id]; record != nil {
		fn(record)
	}
}

func (d *webDownloads) get(id string) (*webDownloadRecord, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	record := d.records[id]
	if record == nil {
		return nil, false
	}
	copy := *record
	return &copy, true
}

func runHostedWeb(port string) error {
	if port == "" {
		return errors.New("Vercel did not provide PORT")
	}
	return runPublicWeb(":"+port, []string{os.Getenv("VERCEL_URL"), os.Getenv("VERCEL_PROJECT_PRODUCTION_URL")})
}

func runPublicWeb(addr string, hosts []string) error {
	app, err := newWebApp(newClient(), newAppleChartClient())
	if err != nil {
		return err
	}
	app.publicHosts = make(map[string]bool)
	for _, host := range hosts {
		if host := strings.ToLower(host); host != "" {
			app.publicHosts[host] = true
		}
	}
	if len(app.publicHosts) == 0 {
		return errors.New("a public hostname is required")
	}
	server := &http.Server{
		Addr: addr, Handler: app.handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	return server.ListenAndServe()
}

func runWeb(addr string) error {
	if addr == "" {
		addr = webDefaultAddr
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("web address %q: %w", addr, err)
	}
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return errors.New("the web app only accepts loopback addresses such as 127.0.0.1:8765")
	}
	handler, err := newWebHandler()
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	baseURL := "http://" + listener.Addr().String()
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(listener)
	}()
	fmt.Fprintf(os.Stderr, "flac_dl web is running at %s\n", baseURL)
	openBrowser(baseURL)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	select {
	case serverErr := <-errCh:
		if errors.Is(serverErr, http.ErrServerClosed) {
			return nil
		}
		return serverErr
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return err
		}
		return nil
	}
}

func openBrowser(page string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", page)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", page)
	default:
		cmd = exec.Command("xdg-open", page)
	}
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "open this URL in a browser: %s (%v)\n", page, err)
		return
	}
	go func() {
		if err := cmd.Wait(); err != nil {
			fmt.Fprintf(os.Stderr, "open this URL in a browser: %s (%v)\n", page, err)
		}
	}()
}
