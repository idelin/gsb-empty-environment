package ptrauth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
)

// Authenticator is the injected message-authentication interface. The
// runtime supplies a keyed implementation (see NewHMACAuthenticator) so the
// pointer authentication policy can be unit-tested with real cryptographic
// material without hard-coding any secret in the package.
//
// Every implementation MUST be safe for concurrent use.
type Authenticator interface {
	// Tag produces an authentication code binding together the pointer
	// address, the object generation, the access kind bitmask, the call
	// context and the authorized span.
	Tag(address, generation uint64, kind Kind, ctx ContextID, span uint64) []byte

	// Verify checks a tag in constant time relative to the message content.
	Verify(address, generation uint64, kind Kind, ctx ContextID, span uint64, tag []byte) bool
}

// ContextID identifies the call context (goroutine/task/domain pair) in
// which a pointer was authenticated. A pointer never silently crosses a
// context boundary; Recontext re-authenticates it explicitly.
type ContextID string

// hmacAuthenticator implements Authenticator using HMAC over a canonical
// fixed-width encoding. HMAC is keyed, so a pointer minted by one runtime
// instance cannot be forged by a holder of another instance's tags.
type hmacAuthenticator struct {
	key []byte
}

// NewHMACAuthenticator builds an HMAC-SHA256 Authenticator with the supplied
// secret key. The key must be at least 16 bytes; prefer 32 bytes of random
// material. Use GenerateHMACKey to create one.
func NewHMACAuthenticator(key []byte) (Authenticator, error) {
	if len(key) < 16 {
		return nil, ErrWeakKey
	}
	copied := make([]byte, len(key))
	copy(copied, key)
	return &hmacAuthenticator{key: copied}, nil
}

// GenerateHMACKey returns 32 bytes of cryptographically secure random key
// material suitable for NewHMACAuthenticator.
func GenerateHMACKey() ([]byte, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	return key, nil
}

// canonicalMessage serializes every authenticated attribute into a
// deterministic, length-ambiguity-free byte string. The context length is
// encoded before the context bytes so two different context strings can
// never produce the same preimage.
func canonicalMessage(address, generation uint64, kind Kind, ctx ContextID, span uint64) []byte {
	contextBytes := []byte(ctx)
	msg := make([]byte, 0, 8*5+len(contextBytes))
	var word [8]byte
	binary.BigEndian.PutUint64(word[:], address)
	msg = append(msg, word[:]...)
	binary.BigEndian.PutUint64(word[:], generation)
	msg = append(msg, word[:]...)
	normalized, err := kind.normalize()
	if err != nil {
		panic(err)
	}
	binary.BigEndian.PutUint64(word[:], uint64(normalized))
	msg = append(msg, word[:]...)
	binary.BigEndian.PutUint64(word[:], span)
	msg = append(msg, word[:]...)
	binary.BigEndian.PutUint64(word[:], uint64(len(contextBytes)))
	msg = append(msg, word[:]...)
	msg = append(msg, contextBytes...)
	return msg
}

func (h *hmacAuthenticator) Tag(address, generation uint64, kind Kind, ctx ContextID, span uint64) []byte {
	mac := hmac.New(sha256.New, h.key)
	mac.Write(canonicalMessage(address, generation, kind, ctx, span))
	return mac.Sum(nil)
}

func (h *hmacAuthenticator) Verify(address, generation uint64, kind Kind, ctx ContextID, span uint64, tag []byte) bool {
	if len(tag) == 0 {
		return false
	}
	expected := h.Tag(address, generation, kind, ctx, span)
	return subtle.ConstantTimeCompare(expected, tag) == 1
}
