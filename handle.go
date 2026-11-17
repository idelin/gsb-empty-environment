package netsec

import "crypto/sha256"

// Handle is an immutable view of one release. All of its fields come from a
// single committed version, so callers can never observe new content with an
// old label, or a digest that does not match the content they received.
//
// Handle values are safe for concurrent use. Content is copied on every call,
// so callers cannot mutate the release held by the Store.
type Handle struct {
	key     string
	version uint64
	digest  Digest
	label   Label
	content []byte
}

// Key returns the object key.
func (h *Handle) Key() string {
	if h == nil {
		return ""
	}
	return h.key
}

// Version returns the monotonically increasing release version.
func (h *Handle) Version() uint64 {
	if h == nil {
		return 0
	}
	return h.version
}

// Digest returns the SHA-256 digest of the release content.
func (h *Handle) Digest() Digest {
	if h == nil {
		return Digest{}
	}
	return h.digest
}

// Label returns the security label committed with this release.
func (h *Handle) Label() Label {
	if h == nil {
		return Label{}
	}
	return h.label
}

// Content returns a defensive copy of the release content.
func (h *Handle) Content() []byte {
	if h == nil {
		return nil
	}
	return append([]byte(nil), h.content...)
}

// Size returns the content length in bytes.
func (h *Handle) Size() int {
	if h == nil {
		return 0
	}
	return len(h.content)
}

// Verify reports whether the handle has a usable label and its content still
// matches the committed digest.
func (h *Handle) Verify() bool {
	if h == nil || !h.label.Valid() {
		return false
	}
	return sha256.Sum256(h.content) == [sha256.Size]byte(h.digest)
}
