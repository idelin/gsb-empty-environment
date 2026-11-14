package netsec

import (
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// Handler serves files through a Resolver. Directory listings, conditional
// requests (If-Modified-Since, If-Range) and range requests all operate on
// the single Resolved result produced per request.
type Handler struct {
	resolver *Resolver
}

// NewHandler returns an http.Handler serving content via r.
func NewHandler(r *Resolver) *Handler {
	return &Handler{resolver: r}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	res, err := h.resolver.Resolve(req.URL.EscapedPath())
	if err != nil {
		if errors.Is(err, ErrBadRequest) {
			http.Error(w, "Bad Request", http.StatusBadRequest)
			return
		}
		// Out-of-root and nonexistent paths get the exact same response.
		http.NotFound(w, req)
		return
	}

	f, err := res.Open()
	if err != nil {
		http.NotFound(w, req)
		return
	}
	defer f.Close()

	if res.Info.IsDir() {
		h.serveDir(w, req, res, f)
		return
	}
	// ServeContent handles conditional headers and Range requests using
	// only the already-resolved file and its metadata.
	http.ServeContent(w, req, res.Info.Name(), res.Info.ModTime(), f)
}

// serveDir renders a minimal listing of the resolved directory.
func (h *Handler) serveDir(w http.ResponseWriter, req *http.Request, res *Resolved, f *os.File) {
	entries, err := f.ReadDir(-1)
	if err != nil {
		http.NotFound(w, req)
		return
	}
	base := strings.TrimSuffix(req.URL.Path, "/")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, "<!doctype html><title>Index of %s</title><h1>Index of %s</h1><ul>\n",
		html.EscapeString(req.URL.Path), html.EscapeString(req.URL.Path))
	for _, e := range entries {
		name := e.Name()
		if strings.ContainsAny(name, "\x00\\") {
			continue
		}
		display := name
		if e.IsDir() {
			display += "/"
		}
		fmt.Fprintf(w, "<li><a href=\"%s\">%s</a></li>\n",
			base+"/"+url.PathEscape(name), html.EscapeString(display))
	}
	fmt.Fprint(w, "</ul>\n")
}
