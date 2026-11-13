package strictp256

import (
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/binary"
)

// RequestIDSize is the byte length of a request identifier.
const RequestIDSize = sha256.Size

// CompressedPublicKey returns the canonical SEC 1 compressed encoding of a
// P-256 public key: a single 33-byte value (prefix 0x02/0x03 followed by the
// big-endian X coordinate). It is the only key representation fed into request
// identifiers and revocation indexes.
func CompressedPublicKey(pub *ecdsa.PublicKey) ([]byte, error) {
	if err := validatePublicKey(pub); err != nil {
		return nil, err
	}
	params := pub.Curve.Params()
	coordinate := make([]byte, (params.BitSize+7)/8)
	out := make([]byte, 1+len(coordinate))
	if pub.Y.Bit(0) == 0 {
		out[0] = 0x02
	} else {
		out[0] = 0x03
	}
	pub.X.FillBytes(coordinate)
	copy(out[1:], coordinate)
	return out, nil
}

// RequestID derives the stable identifier of a request from content only. The
// signature is deliberately absent, so the (r, s) and (r, n-s) forms of one
// authorization always collapse onto the same ID:
//
//	SHA256(DomainLabel || canonical compressed key ||
//	       len_be64(message) || message)
//
// The domain label is a fixed constant and the compressed key is a fixed
// 33-byte encoding, while the message is explicitly big-endian length-prefixed
// to make the concatenation unambiguous.
func RequestID(pub *ecdsa.PublicKey, msg []byte) ([RequestIDSize]byte, error) {
	var zero [RequestIDSize]byte
	key, err := CompressedPublicKey(pub)
	if err != nil {
		return zero, err
	}

	h := sha256.New()
	h.Write([]byte(DomainLabel))
	var lengthBuf [8]byte
	h.Write(key)
	binary.BigEndian.PutUint64(lengthBuf[:], uint64(len(msg)))
	h.Write(lengthBuf[:])
	h.Write(msg)

	var id [RequestIDSize]byte
	copy(id[:], h.Sum(nil))
	return id, nil
}

// indexKey binds a request to its sender: compressed public key followed by the
// request ID. Revocation and dedup therefore target (public key, message ID),
// never signature bytes.
func indexKey(pub *ecdsa.PublicKey, id [RequestIDSize]byte) (string, error) {
	key, err := CompressedPublicKey(pub)
	if err != nil {
		return "", err
	}
	buf := make([]byte, 0, len(key)+RequestIDSize)
	buf = append(buf, key...)
	buf = append(buf, id[:]...)
	return string(buf), nil
}

// Registry deduplicates concurrent and repeated submissions. A given
// (public key, request ID) pair is registered atomically at most once; valid
// duplicates return the original handler result without re-executing it. A
// signature that fails strict verification never reaches the registry and so
// never consumes an identifier.
type Registry[T any] struct {
	mu      chan struct{}
	entries map[string]*entry[T]
	revoked map[string]struct{}
}

type entry[T any] struct {
	done chan struct{}
	val  T
	err  error
}

// NewRegistry creates an empty Registry.
func NewRegistry[T any]() *Registry[T] {
	return &Registry[T]{
		mu:      make(chan struct{}, 1),
		entries: make(map[string]*entry[T]),
		revoked: make(map[string]struct{}),
	}
}

func (r *Registry[T]) lock()   { r.mu <- struct{}{} }
func (r *Registry[T]) unlock() { <-r.mu }

// Handler executes the side effect for a freshly registered request.
type Handler[T any] func() (T, error)

// Submit verifies sig against pub/msg under the strict rules, then runs handler
// exactly once per (public key, request ID). Concurrent or repeated valid
// submissions block until the first completes and receive its result. A
// revoked pair returns ErrRevoked; a handler error releases the slot so the
// request can be retried.
func (r *Registry[T]) Submit(pub *ecdsa.PublicKey, msg, sig []byte, handler Handler[T]) (T, error) {
	if err := VerifyStrict(pub, msg, sig); err != nil {
		var zero T
		return zero, err
	}
	id, err := RequestID(pub, msg)
	if err != nil {
		var zero T
		return zero, err
	}
	key, err := indexKey(pub, id)
	if err != nil {
		var zero T
		return zero, err
	}

	r.lock()
	if _, ok := r.revoked[key]; ok {
		r.unlock()
		var zero T
		return zero, ErrRevoked
	}
	if existing, ok := r.entries[key]; ok {
		r.unlock()
		return r.wait(existing, key)
	}
	current := &entry[T]{done: make(chan struct{})}
	r.entries[key] = current
	r.unlock()

	val, handlerErr := handler()

	r.lock()
	if handlerErr != nil {
		// Failure must not occupy the identifier: drop it so a retry can run.
		delete(r.entries, key)
	} else {
		current.val = val
		current.err = nil
	}
	close(current.done)
	r.unlock()
	return val, handlerErr
}

// wait blocks on an in-flight or completed entry, re-checking revocation after
// wake-up so a revoked duplicate cannot observe a cached value.
func (r *Registry[T]) wait(e *entry[T], key string) (T, error) {
	<-e.done
	r.lock()
	_, revoked := r.revoked[key]
	r.unlock()
	if revoked {
		var zero T
		return zero, ErrRevoked
	}
	return e.val, e.err
}

// Revoke marks the (public key, message) pair as revoked. Because the index is
// keyed by public key and content-derived request ID, both S variants and any
// future re-signature of the same message by that key are blocked.
func (r *Registry[T]) Revoke(pub *ecdsa.PublicKey, msg []byte) error {
	id, err := RequestID(pub, msg)
	if err != nil {
		return err
	}
	key, err := indexKey(pub, id)
	if err != nil {
		return err
	}
	r.lock()
	r.revoked[key] = struct{}{}
	r.unlock()
	return nil
}

// IsRevoked reports whether the (public key, message) pair is currently
// revoked. It is primarily intended for tests and diagnostics.
func (r *Registry[T]) IsRevoked(pub *ecdsa.PublicKey, msg []byte) bool {
	id, err := RequestID(pub, msg)
	if err != nil {
		return false
	}
	key, err := indexKey(pub, id)
	if err != nil {
		return false
	}
	r.lock()
	_, ok := r.revoked[key]
	r.unlock()
	return ok
}
