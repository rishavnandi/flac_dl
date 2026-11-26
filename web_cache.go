package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Only finite public GET pages use this cache, never streams or download jobs.
type webPageCache struct {
	mu      sync.Mutex
	entries map[string]*webPageEntry
}

type webPageEntry struct {
	response   *http.Response
	body       []byte
	stored     time.Time
	expires    time.Time
	staleUntil time.Time
	pending    chan struct{}
}

func (c *webPageCache) handler(ttl time.Duration, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || len(r.URL.RequestURI()) > 2048 {
			next(w, r)
			return
		}
		key := r.Host + r.URL.RequestURI() + "\n" + strconv.FormatBool(r.Header.Get("HX-Request") == "true") +
			"\n" + strconv.FormatBool(r.Header.Get("HX-History-Restore-Request") == "true")
		c.mu.Lock()
		if c.entries == nil {
			c.entries = make(map[string]*webPageEntry)
		}
		entry := c.entries[key]
		now := time.Now()
		if entry != nil && entry.response != nil && now.Before(entry.staleUntil) {
			if now.After(entry.expires) && entry.pending == nil {
				entry.pending = make(chan struct{})
				go c.load(key, entry, ttl, next, r)
			}
			response, body, stored := entry.response, entry.body, entry.stored
			c.mu.Unlock()
			writeCachedPage(w, response, body, stored)
			return
		}
		if entry == nil {
			if len(c.entries) >= 128 {
				var oldestKey string
				var oldest time.Time
				for k, e := range c.entries {
					if e.pending == nil && (oldestKey == "" || e.stored.Before(oldest)) {
						oldestKey, oldest = k, e.stored
					}
				}
				if oldestKey == "" {
					c.mu.Unlock()
					next(w, r)
					return
				}
				delete(c.entries, oldestKey)
			}
			entry = &webPageEntry{}
			c.entries[key] = entry
		}
		if entry.pending == nil {
			entry.pending = make(chan struct{})
			go c.load(key, entry, ttl, next, r)
		}
		pending := entry.pending
		c.mu.Unlock()
		select {
		case <-r.Context().Done():
			return
		case <-pending:
			c.mu.Lock()
			response, body, stored := entry.response, entry.body, entry.stored
			c.mu.Unlock()
			writeCachedPage(w, response, body, stored)
		}
	}
}

func (c *webPageCache) load(key string, entry *webPageEntry, ttl time.Duration, next http.HandlerFunc, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 30*time.Second)
	defer cancel()
	recorder := httptest.NewRecorder()
	next(recorder, r.Clone(ctx))
	response, body := recorder.Result(), recorder.Body.Bytes()
	// renderError returns HTTP 200 for HTMX, but explicitly marks it no-store.
	cacheable := (response.StatusCode == http.StatusOK || response.StatusCode == http.StatusFound) &&
		strings.Contains(response.Header.Get("Cache-Control"), "public") && len(body) <= 512<<10
	c.mu.Lock()
	defer c.mu.Unlock()
	if cacheable {
		entry.response, entry.body, entry.stored = response, body, time.Now()
		entry.expires = entry.stored.Add(ttl)
		entry.staleUntil = entry.expires.Add(24 * time.Hour)
	} else if entry.response == nil || time.Now().After(entry.staleUntil) {
		entry.response, entry.body = response, body
		delete(c.entries, key)
	} else {
		entry.expires = time.Now().Add(30 * time.Second)
	}
	close(entry.pending)
	entry.pending = nil
}

func writeCachedPage(w http.ResponseWriter, response *http.Response, body []byte, stored time.Time) {
	for key, values := range response.Header {
		w.Header()[key] = append([]string(nil), values...)
	}
	if !stored.IsZero() {
		w.Header().Set("Age", strconv.Itoa(max(0, int(time.Since(stored).Seconds()))))
	}
	w.WriteHeader(response.StatusCode)
	_, _ = w.Write(body)
}

func cachePublic(w http.ResponseWriter, seconds int) {
	w.Header().Set("Cache-Control", "public, max-age="+strconv.Itoa(seconds)+", stale-while-revalidate=86400, stale-if-error=86400")
	w.Header().Set("Vercel-CDN-Cache-Control", "public, s-maxage="+strconv.Itoa(seconds)+", stale-while-revalidate=86400, stale-if-error=86400")
}
