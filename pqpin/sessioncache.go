package pqpin

import (
	"crypto/tls"
	"sync"
)

// originMarkerPrefix tags a tls.SessionState.Extra entry as pqpin origin
// metadata. Entries are id-versioned so future formats can be distinguished
// and foreign Extra entries are left untouched, as required by crypto/tls.
var originMarkerPrefix = []byte("pqpin-origin-v1:")

// Origin describes the key exchange used by the handshake that minted a
// resumable session.
type Origin int

const (
	// OriginUnknown means the session predates the pinner or came from a
	// cache the pinner does not control.
	OriginUnknown Origin = iota
	// OriginClassic means the original handshake did not use a hybrid group.
	OriginClassic
	// OriginHybrid means the original handshake negotiated a hybrid group.
	OriginHybrid
)

// originCache tracks, for in-flight connections, whether the handshake that
// is about to store a ticket was itself hybrid. crypto/tls calls
// ClientSessionCache.Put after the Finished messages are verified but does not
// hand the connection state to the cache, so the dialer registers the answer
// here under the same session key before the handshake completes.
type originCache struct {
	mu      sync.Mutex
	pending map[string]Origin
}

func newOriginCache() *originCache {
	return &originCache{pending: make(map[string]Origin)}
}

// setPending records the origin of the connection currently completing under
// sessionKey.
func (c *originCache) setPending(sessionKey string, origin Origin) {
	c.mu.Lock()
	c.pending[sessionKey] = origin
	c.mu.Unlock()
}

// takePending returns and clears the origin registered for sessionKey.
func (c *originCache) takePending(sessionKey string) Origin {
	c.mu.Lock()
	defer c.mu.Unlock()
	if origin, ok := c.pending[sessionKey]; ok {
		delete(c.pending, sessionKey)
		return origin
	}
	return OriginUnknown
}

// originSessionCache wraps a tls.ClientSessionCache and records, inside the
// serialized SessionState, whether the originating handshake was hybrid. The
// marker travels with the session state across process restarts because it is
// stored in SessionState.Extra.
type originSessionCache struct {
	inner   tls.ClientSessionCache
	origins *originCache
}

// WrapSessionCache returns a tls.ClientSessionCache that decorates sessions
// with pqpin origin metadata. inner may be nil, in which case crypto/tls uses
// its own default LRU cache semantics only for sessions whose Put is called.
func WrapSessionCache(inner tls.ClientSessionCache, origins *originCache) tls.ClientSessionCache {
	if inner == nil {
		inner = tls.NewLRUClientSessionCache(32)
	}
	if origins == nil {
		origins = newOriginCache()
	}
	if wrapped, ok := inner.(*originSessionCache); ok {
		return wrapped
	}
	return &originSessionCache{inner: inner, origins: origins}
}

func (c *originSessionCache) Get(sessionKey string) (*tls.ClientSessionState, bool) {
	css, ok := c.inner.Get(sessionKey)
	if !ok || css == nil {
		return css, ok
	}
	return css, true
}

func (c *originSessionCache) Put(sessionKey string, css *tls.ClientSessionState) {
	if css == nil {
		c.inner.Put(sessionKey, nil)
		return
	}
	ticket, state, err := css.ResumptionState()
	if err != nil || state == nil {
		// Unparseable state: pass through unchanged rather than drop data.
		c.inner.Put(sessionKey, css)
		return
	}

	origin := c.origins.takePending(sessionKey)
	if origin == OriginUnknown {
		// Preserve any marker already carried by the state; a session from
		// an older pqpin process keeps its recorded origin.
		origin = readOrigin(state)
	}
	state.Extra = setOriginMarker(state.Extra, origin)

	rebuilt, err := tls.NewResumptionState(ticket, state)
	if err != nil {
		c.inner.Put(sessionKey, css)
		return
	}
	c.inner.Put(sessionKey, rebuilt)
}

// ResumeOrigin inspects a cached session and reports the origin recorded for
// it. Sessions without a marker return OriginUnknown.
func ResumeOrigin(cache tls.ClientSessionCache, sessionKey string) Origin {
	if cache == nil {
		return OriginUnknown
	}
	css, ok := cache.Get(sessionKey)
	if !ok || css == nil {
		return OriginUnknown
	}
	_, state, err := css.ResumptionState()
	if err != nil || state == nil {
		return OriginUnknown
	}
	return readOrigin(state)
}

func readOrigin(state *tls.SessionState) Origin {
	for _, extra := range state.Extra {
		if len(extra) != len(originMarkerPrefix)+1 {
			continue
		}
		if string(extra[:len(originMarkerPrefix)]) != string(originMarkerPrefix) {
			continue
		}
		switch extra[len(originMarkerPrefix)] {
		case byte(OriginClassic):
			return OriginClassic
		case byte(OriginHybrid):
			return OriginHybrid
		}
	}
	return OriginUnknown
}

func setOriginMarker(extra [][]byte, origin Origin) [][]byte {
	marker := make([]byte, len(originMarkerPrefix)+1)
	copy(marker, originMarkerPrefix)
	marker[len(originMarkerPrefix)] = byte(origin)

	out := extra[:0:0]
	for _, entry := range extra {
		if len(entry) >= len(originMarkerPrefix) &&
			string(entry[:len(originMarkerPrefix)]) == string(originMarkerPrefix) {
			continue // replace any previous marker instead of duplicating
		}
		out = append(out, entry)
	}
	return append(out, marker)
}
