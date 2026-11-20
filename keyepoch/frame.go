// Package keyepoch implements a network-free TLS 1.3 KeyUpdate state machine
// for a record-layer host.
//
// The component coordinates independent read and write key generations
// ("epochs") around two small queues: an inbound read queue and an outbound
// write queue. Application data, KeyUpdate messages and close notifications
// may interleave on both queues. All external effects (peer authentication and
// record write-out) are expressed through injected callbacks, so the component
// never opens a socket and remains a pure, deterministic function of an
// ordered event stream. See Replay for independent state verification.
package keyepoch

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
)

// FrameKind enumerates the record-layer frame types modeled by the component.
type FrameKind uint8

const (
	// KindApp is an application-data record.
	KindApp FrameKind = iota + 1
	// KindKeyUpdate is a TLS KeyUpdate record.
	KindKeyUpdate
	// KindClose is a close_notify record.
	KindClose
)

// String returns the canonical short name of the frame kind.
func (k FrameKind) String() string {
	switch k {
	case KindApp:
		return "app"
	case KindKeyUpdate:
		return "keyupdate"
	case KindClose:
		return "close"
	default:
		return fmt.Sprintf("unknown(%d)", uint8(k))
	}
}

// RequestUpdate marks a KeyUpdate frame that asks the peer to reply with its
// own KeyUpdate (TLS 1.3 KeyUpdateRequest).
const RequestUpdate = true

// Frame is one record-layer message together with the key generation under
// which it was protected. Frames are value types so that snapshots and event
// replay remain allocation-stable and comparable.
type Frame struct {
	// Kind is the record type.
	Kind FrameKind
	// Epoch is the key generation (0-based) that protects this frame.
	Epoch uint64
	// Seq is the per-epoch record sequence number.
	Seq uint64
	// MsgID is the unique queue identity assigned at enqueue time. Repeated
	// transmission attempts for the same frame keep the same MsgID, which is
	// how ambiguous write outcomes are reconciled without duplicating updates.
	MsgID string
	// Request is meaningful only for KeyUpdate frames.
	Request bool
	// Payload is the opaque application-data bytes (empty for control frames).
	Payload []byte
	// Tag is the HMAC authenticator computed over the canonical frame bytes.
	Tag []byte
}

// frameLabel renders the stable, unambiguous text used both for key derivation
// input and for authenticated-content computation.
func frameLabel(f Frame) string {
	return fmt.Sprintf("v1|%s|epoch=%d|seq=%d|msgid=%s|request=%t|payload=%x",
		f.Kind, f.Epoch, f.Seq, f.MsgID, f.Request, f.Payload)
}

// hmacSHA256 computes HMAC-SHA256(key, data).
func hmacSHA256(key, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}

// DeriveKey derives the record protection key for a directional secret at the
// given epoch. Advancing an epoch means replacing the active key with the
// output of the next derivation; old keys remain available only long enough to
// authenticate frames already queued under them.
func DeriveKey(secret []byte, epoch uint64) []byte {
	var epochBytes [8]byte
	binary.BigEndian.PutUint64(epochBytes[:], epoch)
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("tls13 keyepoch record key v1"))
	mac.Write(epochBytes[:])
	return mac.Sum(nil)
}

// SignFrame authenticates the frame content under the key for secret/epoch and
// stores the resulting tag on the frame, returning the same frame by value.
func SignFrame(f Frame, secret []byte) Frame {
	f.Tag = hmacSHA256(DeriveKey(secret, f.Epoch), []byte(frameLabel(f)))
	return f
}

// VerifyFrame checks the frame tag under the key for secret/epoch. It never
// consults any other state: revocation, closure and sequence-window checks are
// the engine's responsibility and remain separate from cryptographic
// authentication.
func VerifyFrame(f Frame, secret []byte) bool {
	if len(f.Tag) == 0 {
		return false
	}
	want := hmacSHA256(DeriveKey(secret, f.Epoch), []byte(frameLabel(f)))
	return hmac.Equal(want, f.Tag)
}
