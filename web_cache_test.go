package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPageCacheCoalescesAndSeparatesRepresentations(t *testing.T) {
	var calls atomic.Int32
	app, _ := testWebApp(t, func(string, url.Values) string {
		calls.Add(1)
		return `{"code":0,"data":{"total":1,"list":[` + trackJSON(42, "Cached Song") + `]}}`
	})
	handler := app.handler()
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, webTestRequest(http.MethodGet, "/search?q=test", nil))
			if !strings.Contains(res.Body.String(), "Cached Song") {
				t.Error("concurrent request lost its response")
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("12 identical requests made %d upstream calls", calls.Load())
	}
	for _, restore := range []string{"", "true"} {
		req := webTestRequest(http.MethodGet, "/search?q=test", nil)
		req.Header.Set("HX-Request", "true")
		req.Header.Set("HX-History-Restore-Request", restore)
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if strings.Contains(res.Body.String(), "<!doctype html>") != (restore == "true") {
			t.Fatal("cache confused a fragment with a full-page history restore")
		}
		if !strings.Contains(strings.Join(res.Header().Values("Vary"), ","), "HX-History-Restore-Request") {
			t.Fatal("browser/CDN cache does not vary on history restoration")
		}
	}
	if calls.Load() != 3 {
		t.Fatalf("representations were not independently cached: %d calls", calls.Load())
	}
	req := webTestRequest(http.MethodGet, "/search?q=test", nil)
	req.Host = "attacker.example"
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatal("cache bypassed host validation")
	}
}

func TestPageCacheDoesNotStoreErrorsOrOversizedResponses(t *testing.T) {
	for _, mode := range []string{"error", "oversized"} {
		t.Run(mode, func(t *testing.T) {
			var calls int
			var cache webPageCache
			handler := cache.handler(time.Hour, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if mode == "error" {
					w.Header().Set("Cache-Control", "no-store")
					// HTMX error pages intentionally have status 200.
					fmt.Fprint(w, "upstream failed")
				} else {
					cachePublic(w, 3600)
					fmt.Fprint(w, strings.Repeat("x", (512<<10)+1))
				}
			})
			for range 2 {
				res := httptest.NewRecorder()
				handler(res, webTestRequest(http.MethodGet, "/", nil))
				if res.Body.Len() == 0 {
					t.Fatal("uncacheable response was lost")
				}
			}
			if calls != 2 || len(cache.entries) != 0 {
				t.Fatal("uncacheable response was retained")
			}
		})
	}
}

func TestPageCacheKeepsStalePageDuringFailedRefresh(t *testing.T) {
	var calls atomic.Int32
	var cache webPageCache
	started, release := make(chan struct{}), make(chan struct{})
	handler := cache.handler(time.Minute, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			cachePublic(w, 60)
			fmt.Fprint(w, "last good page")
			return
		}
		close(started)
		<-release
		w.Header().Set("Cache-Control", "no-store")
		http.Error(w, "upstream down", http.StatusBadGateway)
	})
	handler(httptest.NewRecorder(), webTestRequest(http.MethodGet, "/", nil))
	cache.mu.Lock()
	for _, entry := range cache.entries {
		entry.expires = time.Now().Add(-time.Minute)
	}
	cache.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	res := httptest.NewRecorder()
	handler(res, webTestRequest(http.MethodGet, "/", nil).WithContext(ctx))
	if res.Body.String() != "last good page" || ctx.Err() != nil {
		t.Fatal("stale page waited for the upstream")
	}
	<-started
	cache.mu.Lock()
	var pending chan struct{}
	for _, entry := range cache.entries {
		pending = entry.pending
	}
	cache.mu.Unlock()
	close(release)
	<-pending
	res = httptest.NewRecorder()
	handler(res, webTestRequest(http.MethodGet, "/", nil))
	if res.Body.String() != "last good page" || calls.Load() != 2 {
		t.Fatal("failed refresh discarded the good page or ignored retry backoff")
	}
}

func TestPageCacheEvictsOldEntries(t *testing.T) {
	var cache webPageCache
	handler := cache.handler(time.Hour, func(w http.ResponseWriter, r *http.Request) {
		cachePublic(w, 3600)
		fmt.Fprint(w, r.URL.Path)
	})
	for i := range 140 {
		handler(httptest.NewRecorder(), webTestRequest(http.MethodGet, fmt.Sprintf("/%d", i), nil))
	}
	if len(cache.entries) != 128 {
		t.Fatalf("cache grew to %d entries", len(cache.entries))
	}
}

func TestStaticAssetsAreVersionedAndConditionallyCached(t *testing.T) {
	app, _ := testWebApp(t, func(string, url.Values) string { return "" })
	handler := app.handler()
	res := httptest.NewRecorder()
	handler.ServeHTTP(res, webTestRequest(http.MethodGet, "/", nil))
	html := res.Body.String()
	start := strings.Index(html, `/static/site.css?v=`)
	if start < 0 {
		t.Fatal("stylesheet has no content version")
	}
	path := strings.SplitN(html[start:], `"`, 2)[0]
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, webTestRequest(http.MethodGet, path, nil))
	if !strings.Contains(res.Header().Get("Cache-Control"), "immutable") {
		t.Fatal("versioned asset does not use immutable browser caching")
	}
	req := webTestRequest(http.MethodGet, path, nil)
	req.Header.Set("If-None-Match", res.Header().Get("ETag"))
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, req)
	if res.Code != http.StatusNotModified {
		t.Fatalf("asset revalidation status = %d", res.Code)
	}
	res = httptest.NewRecorder()
	handler.ServeHTTP(res, webTestRequest(http.MethodGet, "/static/site.css?v=old", nil))
	if strings.Contains(res.Header().Get("Cache-Control"), "immutable") {
		t.Fatal("wrong asset version was cached as immutable")
	}
}
