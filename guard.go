// Package metaguard implements a metadata access restriction component for a
// cloud instance proxy. It issues one-time tokens bound to the path prefix
// and source identity captured at issuance time, and serializes concurrent
// reuse attempts so that exactly one request wins while the rest are rejected
// and recorded as conflicts.
package metaguard

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"strings"
	"sync"
	"time"
)

// RejectReason classifies why a request was denied.
type RejectReason string

const (
	ReasonUnknownToken   RejectReason = "unknown_token"
	ReasonTokenConsumed  RejectReason = "token_consumed"
	ReasonConcurrentUse  RejectReason = "concurrent_reuse"
	ReasonPathTraversal  RejectReason = "path_traversal"
	ReasonCrossVersion   RejectReason = "cross_version"
	ReasonPrefixMismatch RejectReason = "prefix_mismatch"
	ReasonSourceMismatch RejectReason = "source_mismatch"
)

// ErrRejected is returned by Authorize when a request is denied. The
// Rejection value carried with it describes the reason.
var ErrRejected = errors.New("metaguard: request rejected")

// Rejection describes a denied request.
type Rejection struct {
	Reason RejectReason
	Detail string
}

func (e *Rejection) Error() string {
	return fmt.Sprintf("%s: %s", ErrRejected, e.Detail)
}

func (e *Rejection) Unwrap() error { return ErrRejected }

// AuditEntry is one recorded authorization decision.
type AuditEntry struct {
	At       time.Time
	TokenID  string
	Source   string
	Path     string
	Allowed  bool
	Reason   RejectReason
	Detail   string
	Conflict bool // true when the denial lost a concurrent-reuse race
}

type token struct {
	id         string
	pathPrefix string // normalized, trailing-slash form, e.g. "/v1/"
	source     string
	inFlight   bool
	consumed   bool
}

// Guard issues and validates one-time metadata access tokens. It is safe for
// concurrent use.
type Guard struct {
	mu          sync.Mutex
	tokens      map[string]*token
	audit       []AuditEntry
	nowFunc     func() time.Time
	preValidate func() // test hook: runs while the token is in-flight
}

// NewGuard returns an empty Guard.
func NewGuard() *Guard {
	return &Guard{
		tokens:  make(map[string]*token),
		nowFunc: time.Now,
	}
}

// Issue creates a one-time token bound to the given path prefix and source
// identity. The prefix is normalized to a clean, trailing-slash absolute
// form; a prefix containing traversal segments is rejected.
func (g *Guard) Issue(pathPrefix, source string) (string, error) {
	norm, err := normalizePrefix(pathPrefix)
	if err != nil {
		return "", err
	}
	if source == "" {
		return "", errors.New("metaguard: source must not be empty")
	}
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("metaguard: token generation: %w", err)
	}
	id := hex.EncodeToString(raw)

	g.mu.Lock()
	g.tokens[id] = &token{id: id, pathPrefix: norm, source: source}
	g.mu.Unlock()
	return id, nil
}

// Authorize validates a request and consumes the token on success. Calls are
// serialized per token: while one request is being authorized, any
// overlapping request for the same token is rejected with
// ReasonConcurrentUse and the conflict is recorded in the audit log. A
// request arriving after the token was fully consumed is rejected with
// ReasonTokenConsumed.
func (g *Guard) Authorize(tokenID, reqPath, source string) error {
	g.mu.Lock()
	tok, ok := g.tokens[tokenID]
	if !ok {
		g.recordLocked(AuditEntry{
			TokenID: tokenID, Source: source, Path: reqPath,
			Reason: ReasonUnknownToken, Detail: "token not found",
		})
		g.mu.Unlock()
		return &Rejection{Reason: ReasonUnknownToken, Detail: "token not found"}
	}
	if tok.inFlight {
		g.recordLocked(AuditEntry{
			TokenID: tokenID, Source: source, Path: reqPath,
			Reason: ReasonConcurrentUse, Conflict: true,
			Detail: "token is being consumed by a concurrent request",
		})
		g.mu.Unlock()
		return &Rejection{
			Reason: ReasonConcurrentUse,
			Detail: "token is being consumed by a concurrent request",
		}
	}
	if tok.consumed {
		g.recordLocked(AuditEntry{
			TokenID: tokenID, Source: source, Path: reqPath,
			Reason: ReasonTokenConsumed, Detail: "token already consumed",
		})
		g.mu.Unlock()
		return &Rejection{Reason: ReasonTokenConsumed, Detail: "token already consumed"}
	}
	tok.inFlight = true
	g.mu.Unlock()

	if g.preValidate != nil {
		g.preValidate()
	}

	// The token binding is immutable, so validation can run outside the
	// lock while the inFlight flag serializes concurrent reuse.
	rej := validateBinding(tok, reqPath, source)

	g.mu.Lock()
	tok.inFlight = false
	if rej != nil {
		g.recordLocked(AuditEntry{
			TokenID: tokenID, Source: source, Path: reqPath,
			Reason: rej.Reason, Detail: rej.Detail,
		})
		g.mu.Unlock()
		return rej
	}
	tok.consumed = true
	g.recordLocked(AuditEntry{TokenID: tokenID, Source: source, Path: reqPath, Allowed: true})
	g.mu.Unlock()
	return nil
}

// validateBinding checks the request against the token's issuance binding.
// The token is not consumed on failure.
func validateBinding(tok *token, reqPath, source string) *Rejection {
	if source != tok.source {
		return &Rejection{
			Reason: ReasonSourceMismatch,
			Detail: fmt.Sprintf("source %q does not match issuance source %q", source, tok.source),
		}
	}
	cleaned, rej := normalizeRequestPath(reqPath)
	if rej != nil {
		return rej
	}
	if versionOf(cleaned) != versionOf(tok.pathPrefix) {
		return &Rejection{
			Reason: ReasonCrossVersion,
			Detail: fmt.Sprintf("path %q targets a different API version than issued prefix %q", cleaned, tok.pathPrefix),
		}
	}
	if !withinPrefix(cleaned, tok.pathPrefix) {
		return &Rejection{
			Reason: ReasonPrefixMismatch,
			Detail: fmt.Sprintf("path %q escapes issued prefix %q", cleaned, tok.pathPrefix),
		}
	}
	return nil
}

// normalizePrefix cleans an issuance prefix into absolute trailing-slash
// form, rejecting traversal segments.
func normalizePrefix(prefix string) (string, error) {
	if prefix == "" {
		return "", errors.New("metaguard: path prefix must not be empty")
	}
	if !strings.HasPrefix(prefix, "/") {
		return "", fmt.Errorf("metaguard: path prefix %q must be absolute", prefix)
	}
	if hasTraversal(prefix) {
		return "", fmt.Errorf("metaguard: path prefix %q contains traversal", prefix)
	}
	cleaned := path.Clean(prefix)
	if !strings.HasSuffix(cleaned, "/") {
		cleaned += "/"
	}
	return cleaned, nil
}

// normalizeRequestPath cleans a request path, rejecting traversal attempts
// outright.
func normalizeRequestPath(p string) (string, *Rejection) {
	if p == "" || !strings.HasPrefix(p, "/") {
		return "", &Rejection{Reason: ReasonPathTraversal, Detail: fmt.Sprintf("path %q is not absolute", p)}
	}
	if hasTraversal(p) {
		return "", &Rejection{Reason: ReasonPathTraversal, Detail: fmt.Sprintf("path %q contains traversal segment", p)}
	}
	return path.Clean(p), nil
}

// hasTraversal reports whether any raw segment of p is "..".
func hasTraversal(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return true
		}
	}
	return false
}

// versionOf extracts the leading path segment, treated as the API version
// (e.g. "/v1/..." -> "v1"). An unversioned path yields "".
func versionOf(p string) string {
	trimmed := strings.TrimPrefix(p, "/")
	if i := strings.IndexByte(trimmed, '/'); i >= 0 {
		return trimmed[:i]
	}
	return trimmed
}

// withinPrefix reports whether cleaned lies under the normalized prefix.
func withinPrefix(cleaned, prefix string) bool {
	return strings.HasPrefix(cleaned+"/", prefix)
}

func (g *Guard) recordLocked(e AuditEntry) {
	e.At = g.nowFunc()
	g.audit = append(g.audit, e)
}

// AuditLog returns a copy of all recorded decisions in order.
func (g *Guard) AuditLog() []AuditEntry {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]AuditEntry, len(g.audit))
	copy(out, g.audit)
	return out
}
