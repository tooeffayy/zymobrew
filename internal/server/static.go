package server

import (
	"bytes"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"

	"zymobrew/web"
)

// staticHandler serves the SPA bundle embedded by package web.
//
// Routing model:
//   - exact-match static asset (CSS/JS/images) → serve from dist/
//   - any other GET → serve dist/index.html (SPA client-side routing)
//   - /api/* paths never reach here; chi.NotFound handles them with JSON
//     via apiNotFound below.
//
// If dist/index.html is missing (frontend not built), every request
// returns the placeholder so a fresh checkout still boots — `go build`
// works without `npm run build`.
func staticHandler() http.Handler {
	sub, err := fs.Sub(web.DistFS, "dist")
	if err != nil {
		// embed.FS guarantees this can't happen; if it ever does, fail
		// loud so we notice in CI rather than serving a confused 500.
		panic("web: dist subfs: " + err.Error())
	}

	indexBytes, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		// Frontend not built yet — return the placeholder for every
		// non-API GET. The error path matters: a fresh `git clone`
		// should still produce a runnable binary.
		return placeholderHandler()
	}

	// Cache the index.html mtime once at startup. We re-serve the same
	// bytes on every SPA fallback hit; setting Last-Modified once is
	// cheaper than stat-ing the embed FS per request.
	indexModTime := time.Now()

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// /api/* should already be handled before reaching here, but a
		// defensive check keeps a misrouted request from getting a
		// 200/HTML response with a JSON-expecting client.
		if strings.HasPrefix(r.URL.Path, "/api/") {
			apiNotFound(w, r)
			return
		}

		// Try the path as a real file first. path.Clean strips ".."
		// traversal attempts; fs.FS rejects them anyway, but doing it
		// here makes the intent explicit.
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p == "" || p == "index.html" {
			serveIndex(w, r, indexBytes, indexModTime)
			return
		}
		f, err := sub.Open(p)
		if err != nil {
			// Not a static file → SPA route. Hand the index to the
			// client; React Router will pick up the path from
			// window.location.
			serveIndex(w, r, indexBytes, indexModTime)
			return
		}
		defer f.Close()
		stat, err := f.Stat()
		if err != nil || stat.IsDir() {
			serveIndex(w, r, indexBytes, indexModTime)
			return
		}

		// Vite's fingerprinted /assets/* outputs are immutable; we let
		// the client cache them aggressively. The HTML wrapper is *not*
		// fingerprinted and is served above with a normal cache policy.
		if strings.HasPrefix(r.URL.Path, "/assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		if ct := mime.TypeByExtension(path.Ext(p)); ct != "" {
			w.Header().Set("Content-Type", ct)
		}
		// http.ServeContent handles Range, If-Modified-Since, etc.
		// io.ReadSeeker is required; embed.FS files satisfy it.
		rs, ok := f.(readSeeker)
		if !ok {
			// Fallback: read into memory. Embed.FS files are normally
			// seekable, but degrading gracefully is cheaper than
			// reasoning about every fs.FS shape we might be handed.
			data, err := fs.ReadFile(sub, p)
			if err != nil {
				serveIndex(w, r, indexBytes, indexModTime)
				return
			}
			http.ServeContent(w, r, p, stat.ModTime(), bytes.NewReader(data))
			return
		}
		http.ServeContent(w, r, p, stat.ModTime(), rs)
	})
}

type readSeeker interface {
	Read(p []byte) (int, error)
	Seek(offset int64, whence int) (int64, error)
}

func serveIndex(w http.ResponseWriter, r *http.Request, body []byte, mod time.Time) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// index.html points at fingerprinted asset URLs, but the HTML itself
	// changes whenever the SPA is rebuilt — never long-cache it.
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeContent(w, r, "index.html", mod, bytes.NewReader(body))
}

// apiNotFound returns the same JSON shape the rest of the API uses for
// 404s. Wired as chi's NotFoundHandler for /api/* fall-throughs so a
// nonexistent /api/whatever doesn't get the SPA HTML.
func apiNotFound(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
}

// placeholderHandler is the static handler used when the SPA hasn't
// been built. Returns a small HTML page explaining what to do; /api/*
// paths still 404 cleanly.
func placeholderHandler() http.Handler {
	const body = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Zymo</title>
<style>body{font:14px/1.5 system-ui;max-width:36rem;margin:4rem auto;padding:0 1rem;color:#1f1d1a}code{background:#f4f0e8;padding:.1em .35em;border-radius:3px}</style>
</head><body>
<h1>Zymo backend is running</h1>
<p>The web frontend hasn't been built yet. From the repo root:</p>
<pre><code>cd web
npm install
npm run build</code></pre>
<p>Then rebuild the Go binary so the new <code>dist/</code> is embedded.</p>
<p>API: <a href="/api/openapi.yaml">openapi.yaml</a> · <a href="/docs">docs</a></p>
</body></html>`
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			apiNotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write([]byte(body))
	})
}

