// Package ui serves the web UI's static files (docs/part4-ui.md, stage 2): the SvelteKit build,
// embedded into the binary at build time, or a placeholder page when the UI wasn't built.
//
// It is the one approved exemption from the API's response enforcer: it serves only fixed
// files embedded at build time, never touches the store or controllers, and never echoes the
// request. The runtime mounts it on the public listener for every path outside /api/.
package ui

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

// dist holds the UI build (`make ui` writes it; only dist/.gitkeep is committed).
//
//go:embed all:dist
var dist embed.FS

//go:embed placeholder.html
var placeholder []byte

// CSP is the UI's Content-Security-Policy (docs/part4-ui.md, "Security model"). The trusted
// types policy name is the one the HTML sanitizer will create (stages 3 and 7).
const CSP = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self'; " +
	"connect-src 'self'; object-src 'none'; base-uri 'none'; frame-src 'none'; " +
	"frame-ancestors 'none'; form-action 'self'; require-trusted-types-for 'script'; " +
	"trusted-types ao3mirror-html"

const (
	// immutableDir holds SvelteKit's content-hashed assets, cached for a year.
	immutableDir   = "/_app/immutable/"
	cacheImmutable = "public, max-age=31536000, immutable"
	// cacheRevalidate is for every other file: the browser may keep it but asks each time.
	cacheRevalidate = "no-cache"
	// assetDir is SvelteKit's asset directory: a missing file there is 404, never the app.
	assetDir    = "/_app/"
	indexPath   = "/index.html"
	contentHTML = "text/html; charset=utf-8"
)

// contentTypes maps the extensions the build emits to their types. The table is explicit
// rather than mime.TypeByExtension, which reads the host's mime files and so varies by
// machine. Anything else is application/octet-stream.
var contentTypes = map[string]string{
	".html":        contentHTML,
	".js":          "text/javascript; charset=utf-8",
	".mjs":         "text/javascript; charset=utf-8",
	".css":         "text/css; charset=utf-8",
	".json":        "application/json",
	".webmanifest": "application/manifest+json",
	".txt":         "text/plain; charset=utf-8",
	".map":         "application/json",
	".svg":         "image/svg+xml",
	".png":         "image/png",
	".ico":         "image/x-icon",
	".webp":        "image/webp",
	".woff2":       "font/woff2",
}

type file struct {
	body        []byte
	contentType string
	etag        string
}

// Handler serves the UI files. It answers GET and HEAD; any other method is 405.
type Handler struct {
	files map[string]*file
	// index is the page served for "/" and every app route: index.html, or the placeholder.
	index *file
	// Built reports whether a UI build is embedded (false: the placeholder is served).
	Built bool
}

// New serves the embedded UI build.
func New() (*Handler, error) {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		return nil, err
	}
	return NewFS(sub)
}

// NewFS serves the files in fsys, a UI build. Names with a segment starting with "." are
// skipped (dist/.gitkeep). Without an index.html it serves the placeholder page instead.
func NewFS(fsys fs.FS) (*Handler, error) {
	h := &Handler{files: map[string]*file{}}
	err := fs.WalkDir(fsys, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if name != "." && strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("ui: %s: not a regular file", name)
		}
		body, err := fs.ReadFile(fsys, name)
		if err != nil {
			return err
		}
		h.files["/"+name] = newFile(body, path.Ext(name))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("ui: reading the build: %w", err)
	}
	if idx, ok := h.files[indexPath]; ok {
		h.index, h.Built = idx, true
	} else {
		if len(h.files) > 0 {
			return nil, errors.New("ui: the build has files but no index.html")
		}
		h.index = newFile(placeholder, ".html")
	}
	return h, nil
}

func newFile(body []byte, ext string) *file {
	sum := sha256.Sum256(body)
	ct, ok := contentTypes[strings.ToLower(ext)]
	if !ok {
		ct = "application/octet-stream"
	}
	return &file{body: body, contentType: ct, etag: `"` + hex.EncodeToString(sum[:16]) + `"`}
}

// ServeHTTP serves r's path: the file with that name, else 404 for a missing asset (a path
// under /_app/ or whose last segment has a dot), else the app's index page, so deep links
// work. The query is ignored and nothing from the request is echoed.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	hdr := w.Header()
	hdr.Set("Content-Security-Policy", CSP)
	hdr.Set("X-Content-Type-Options", "nosniff")
	hdr.Set("Referrer-Policy", "no-referrer")
	hdr.Set("Cross-Origin-Resource-Policy", "same-origin")
	hdr.Set("Cross-Origin-Opener-Policy", "same-origin")
	hdr.Set("Cache-Control", cacheRevalidate)
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		hdr.Set("Allow", "GET, HEAD")
		plain(w, http.StatusMethodNotAllowed)
		return
	}
	p := r.URL.Path
	f, ok := h.files[p]
	switch {
	case ok:
		if strings.HasPrefix(p, immutableDir) {
			hdr.Set("Cache-Control", cacheImmutable)
		}
	case strings.HasPrefix(p, assetDir) || strings.Contains(path.Base(p), "."):
		plain(w, http.StatusNotFound)
		return
	default:
		f = h.index
	}
	hdr.Set("Content-Type", f.contentType)
	hdr.Set("ETag", f.etag)
	// ServeContent handles HEAD, If-None-Match and ranges. The zero modtime sends no
	// Last-Modified (embedded files have none).
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(f.body))
}

// plain writes a fixed status-text body.
func plain(w http.ResponseWriter, status int) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(http.StatusText(status) + "\n"))
}
