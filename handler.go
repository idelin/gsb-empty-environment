package llhls

import (
	"net/http"
	"strconv"
	"strings"
)

// ServeHTTP serves the LL-HLS media playlist.
//
// It implements Blocking Playlist Reload (_HLS_msn, _HLS_part) and Skip
// Delivery (_HLS_skip). Non-blocking requests return the current playlist
// immediately. Blocking requests wait until the requested part/segment
// appears, the configured timeout elapses (current playlist, 200), or the
// request context is canceled. A request whose MSN is more than one past the
// last playlist MSN is rejected with 400 Bad Request, as required by RFC 8216
// section 6.2.5.2.
func (p *Playlist) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	q := r.URL.Query()
	delta := strings.EqualFold(q.Get("_HLS_skip"), "YES") || q.Get("_HLS_skip") == "v2"

	partStr := q.Get("_HLS_part")
	msnStr := q.Get("_HLS_msn")
	if partStr != "" && msnStr == "" {
		http.Error(w, "_HLS_part requires _HLS_msn", http.StatusBadRequest)
		return
	}

	if msnStr == "" {
		p.writePlaylist(w, r, delta)
		return
	}

	msn, err := strconv.ParseUint(msnStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid _HLS_msn", http.StatusBadRequest)
		return
	}
	part := -1
	if partStr != "" {
		part, err = strconv.Atoi(partStr)
		if err != nil || part < 0 {
			http.Error(w, "invalid _HLS_part", http.StatusBadRequest)
			return
		}
	}

	p.mu.Lock()
	lastMSN := p.segs[len(p.segs)-1].msn
	p.mu.Unlock()

	// The spec forbids asking more than one segment past the last one.
	if msn > lastMSN+1 {
		http.Error(w, "_HLS_msn is more than one Media Sequence Number beyond the playlist",
			http.StatusBadRequest)
		return
	}

	timer := p.cfg.Clock.NewTimer(p.cfg.BlockTimeout)
	defer timer.Stop()

	// A single wait path handles every legal case: an aged-out MSN, an
	// existing segment and a future MSN (lastMSN+1) all resolve through the
	// same readiness/aging condition, or via the timeout.
	p.wait(
		func() bool {
			return p.isReadyLocked(msn, part) || p.isAgedOutLocked(msn)
		},
		timer,
		r.Context().Done(),
	)

	// Whether satisfied, aged out or timed out, the response is the current
	// playlist. A context cancellation surfaces only if writing then fails.
	p.writePlaylist(w, r, delta)
}

func (p *Playlist) writePlaylist(w http.ResponseWriter, r *http.Request, delta bool) {
	body := p.render(delta)
	h := w.Header()
	h.Set("Content-Type", ContentType)
	h.Set("Cache-Control", "no-cache")
	if r.Method == http.MethodHead {
		h.Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(http.StatusOK)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
