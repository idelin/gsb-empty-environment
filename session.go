package featureneg

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
)

// Session binds negotiation results to a concrete run. Any retry or
// downgrade changes the transcript, which invalidates certificates
// issued for earlier parameters.
type Session struct {
	ID        [16]byte
	Retry     uint32
	Downgrade bool
	Local     *Offer
	Remote    *Offer
}

// Transcript is the session summary hash: session ID, retry counter,
// downgrade flag and the canonical encoding of both offers.
func (s *Session) Transcript() [32]byte {
	h := sha256.New()
	h.Write(s.ID[:])
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], s.Retry)
	h.Write(buf[:])
	if s.Downgrade {
		h.Write([]byte{1})
	} else {
		h.Write([]byte{0})
	}
	h.Write(EncodeOffer(s.Local))
	h.Write(EncodeOffer(s.Remote))
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum
}

// ErrStaleCertificate is returned when a certificate was issued for a
// different session transcript (e.g. before a retry or downgrade) and
// must not be used to decode data under the new parameters.
var ErrStaleCertificate = errors.New("featureneg: certificate does not match the current session transcript")

// VerifyCertificate checks that a certificate belongs to this session's
// current transcript.
func (s *Session) VerifyCertificate(c *Certificate) error {
	if c == nil {
		return errors.New("featureneg: nil certificate")
	}
	if c.Transcript != s.Transcript() {
		return ErrStaleCertificate
	}
	return nil
}

// ErrDowngradeRemovesRequired is returned when a downgrade request
// tries to silently drop required capabilities.
var ErrDowngradeRemovesRequired = errors.New("featureneg: downgrade may not remove required features")

// ErrDowngradeAddsOptional is returned when a downgrade request tries
// to widen the optional set instead of narrowing it.
var ErrDowngradeAddsOptional = errors.New("featureneg: downgrade may not add optional features")

// RequestDowngrade replaces the local offer with a narrowed one and
// bumps the retry counter, producing a fresh transcript. Required
// features must be preserved exactly; optional features may only be
// removed. Old certificates become stale and cannot decode new data.
func (s *Session) RequestDowngrade(revised *Offer) error {
	if !sameStringSet(revised.Required, s.Local.Required) {
		return ErrDowngradeRemovesRequired
	}
	if !isSubset(revised.Optional, s.Local.Optional) {
		return ErrDowngradeAddsOptional
	}
	s.Local = revised
	s.Retry++
	s.Downgrade = true
	return nil
}

// BindDecoder returns a Decoder bound to the certificate's transcript.
// It fails if the certificate is stale for this session.
func (s *Session) BindDecoder(c *Certificate) (*Decoder, error) {
	if err := s.VerifyCertificate(c); err != nil {
		return nil, err
	}
	return &Decoder{transcript: c.Transcript}, nil
}

// Decoder decodes length-prefixed frames. It is pure data handling: no
// host code is ever executed, and lengths are validated against the
// remaining input before any allocation.
type Decoder struct {
	transcript [32]byte
}

// Transcript returns the session summary this decoder is bound to.
func (d *Decoder) Transcript() [32]byte { return d.transcript }

// Decode reads one frame: uint32 length + payload. The declared length
// is checked against the remaining bytes before the payload is copied.
func (d *Decoder) Decode(frame []byte) ([]byte, error) {
	r := &reader{buf: frame}
	payload := r.bytes(maxFrameLen)
	if r.err != nil {
		return nil, r.err
	}
	if r.off != len(frame) {
		return nil, errTrailing
	}
	// Copy so callers cannot mutate the input buffer through the result.
	out := make([]byte, len(payload))
	copy(out, payload)
	return out, nil
}

func sameStringSet(a, b []string) bool {
	return isSubset(a, b) && isSubset(b, a)
}

func isSubset(a, b []string) bool {
	set := map[string]bool{}
	for _, s := range b {
		set[s] = true
	}
	for _, s := range a {
		if !set[s] {
			return false
		}
	}
	return true
}
