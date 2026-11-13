package netsec

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
)

// Size of a SHA-256 digest used for HandoffHash.
const handoffHashSize = sha256.Size

// magic and version give the canonical wire encoding an unambiguous,
// domain-separated prefix so these bytes cannot be confused with another
// protocol's structure.
var encodingMagic = [4]byte{'N', 'S', 'T', 'X'}

const encodingVersion byte = 1

// Sentinel errors returned for incomplete or malformed transcripts.
var (
	// ErrEmptyHandoff indicates NewSessionTranscript was called with no
	// handoff bytes to hash.
	ErrEmptyHandoff = errors.New("netsec: handoff bytes must not be empty")
	// ErrEmptyReaderKey indicates the reader public key was missing.
	ErrEmptyReaderKey = errors.New("netsec: reader public key must not be empty")
	// ErrEmptyDeviceKey indicates the device public key was missing.
	ErrEmptyDeviceKey = errors.New("netsec: device public key must not be empty")
	// ErrEmptyHandoffHash indicates the hash of the handoff bytes was missing.
	ErrEmptyHandoffHash = errors.New("netsec: handoff hash must not be empty")
	// ErrHandoffHashLength indicates the supplied handoff hash is not a
	// SHA-256 digest.
	ErrHandoffHashLength = errors.New("netsec: handoff hash must be a SHA-256 digest")
	// ErrMalformedEncoding indicates canonical bytes cannot be parsed.
	ErrMalformedEncoding = errors.New("netsec: malformed session transcript encoding")
)

// SessionTranscript is the data the device signs. It is deliberately separate
// from the handoff data itself: the transcript never embeds the raw handoff
// bytes, only their SHA-256 digest, alongside the two session public keys.
//
// A transcript is accepted if and only if all three fields are present and
// well formed. Missing any one of them is rejected, because the device must
// never sign an incomplete session.
type SessionTranscript struct {
	// HandoffHash is SHA-256 over the handoff bytes. The raw handoff bytes are
	// handoff data, not transcript data, so only their digest is held here.
	HandoffHash []byte
	// ReaderPublicKey is the e-reader public key (raw key bytes).
	ReaderPublicKey []byte
	// DevicePublicKey is the device public key (raw key bytes).
	DevicePublicKey []byte
}

// NewSessionTranscript builds a complete transcript from the raw handoff bytes
// and the two session public keys. It hashes the handoff bytes rather than
// storing them. It returns an error if any of the three required components is
// missing, so a wallet cannot accidentally construct an incomplete transcript.
func NewSessionTranscript(handoff, readerPublicKey, devicePublicKey []byte) (*SessionTranscript, error) {
	if len(handoff) == 0 {
		return nil, ErrEmptyHandoff
	}
	if len(readerPublicKey) == 0 {
		return nil, ErrEmptyReaderKey
	}
	if len(devicePublicKey) == 0 {
		return nil, ErrEmptyDeviceKey
	}
	digest := sha256.Sum256(handoff)
	return &SessionTranscript{
		HandoffHash:     digest[:],
		ReaderPublicKey: append([]byte(nil), readerPublicKey...),
		DevicePublicKey: append([]byte(nil), devicePublicKey...),
	}, nil
}

// NewSessionTranscriptFromHash builds a transcript when the caller already
// holds the SHA-256 digest of the handoff bytes instead of the raw bytes. It
// validates the digest length and both keys before returning.
func NewSessionTranscriptFromHash(handoffHash, readerPublicKey, devicePublicKey []byte) (*SessionTranscript, error) {
	if len(handoffHash) == 0 {
		return nil, ErrEmptyHandoffHash
	}
	if len(handoffHash) != handoffHashSize {
		return nil, ErrHandoffHashLength
	}
	if len(readerPublicKey) == 0 {
		return nil, ErrEmptyReaderKey
	}
	if len(devicePublicKey) == 0 {
		return nil, ErrEmptyDeviceKey
	}
	return &SessionTranscript{
		HandoffHash:     append([]byte(nil), handoffHash...),
		ReaderPublicKey: append([]byte(nil), readerPublicKey...),
		DevicePublicKey: append([]byte(nil), devicePublicKey...),
	}, nil
}

// Validate reports whether the transcript carries all three required items.
// It returns nil only when the handoff hash, reader public key and device
// public key are all present and the hash is a well-formed SHA-256 digest.
// This is the "missing any one fails" check.
func (t *SessionTranscript) Validate() error {
	if t == nil {
		return ErrMalformedEncoding
	}
	if len(t.HandoffHash) == 0 {
		return ErrEmptyHandoffHash
	}
	if len(t.HandoffHash) != handoffHashSize {
		return ErrHandoffHashLength
	}
	if len(t.ReaderPublicKey) == 0 {
		return ErrEmptyReaderKey
	}
	if len(t.DevicePublicKey) == 0 {
		return ErrEmptyDeviceKey
	}
	return nil
}

// Complete reports whether the transcript may be signed or accepted. It is a
// convenience wrapper around Validate; only an all-three-present transcript
// returns true.
func (t *SessionTranscript) Complete() bool {
	return t.Validate() == nil
}

// SigningInput returns the deterministic canonical bytes the device should sign
// for this transcript. It fails if the transcript is incomplete, so a wallet
// can never produce a signature over an incomplete session.
func (t *SessionTranscript) SigningInput() ([]byte, error) {
	if err := t.Validate(); err != nil {
		return nil, err
	}
	return t.encode(), nil
}

// Encode serializes the transcript to its canonical, length-prefixed encoding.
// It refuses to encode an incomplete transcript.
func (t *SessionTranscript) Encode() ([]byte, error) {
	if err := t.Validate(); err != nil {
		return nil, err
	}
	return t.encode(), nil
}

// encode emits the canonical form. Callers must have validated first.
func (t *SessionTranscript) encode() []byte {
	out := make([]byte, 0, headerSize(t))
	out = append(out, encodingMagic[:]...)
	out = append(out, encodingVersion)
	out = appendLengthPrefixed(out, t.HandoffHash)
	out = appendLengthPrefixed(out, t.ReaderPublicKey)
	out = appendLengthPrefixed(out, t.DevicePublicKey)
	return out
}

// Decode parses a canonical transcript and validates it before returning. A
// truncated, trailing-garbage or incomplete encoding is rejected.
func Decode(data []byte) (*SessionTranscript, error) {
	if len(data) < len(encodingMagic)+1 {
		return nil, ErrMalformedEncoding
	}
	for i := range encodingMagic {
		if data[i] != encodingMagic[i] {
			return nil, ErrMalformedEncoding
		}
	}
	pos := len(encodingMagic)
	if data[pos] != encodingVersion {
		return nil, ErrMalformedEncoding
	}
	pos++

	handoffHash, n, err := readLengthPrefixed(data, pos)
	if err != nil {
		return nil, err
	}
	pos += n
	readerKey, n, err := readLengthPrefixed(data, pos)
	if err != nil {
		return nil, err
	}
	pos += n
	deviceKey, n, err := readLengthPrefixed(data, pos)
	if err != nil {
		return nil, err
	}
	pos += n
	if pos != len(data) {
		return nil, ErrMalformedEncoding
	}
	return NewSessionTranscriptFromHash(handoffHash, readerKey, deviceKey)
}

func headerSize(t *SessionTranscript) int {
	return len(encodingMagic) + 1 +
		fieldSize(len(t.HandoffHash)) +
		fieldSize(len(t.ReaderPublicKey)) +
		fieldSize(len(t.DevicePublicKey))
}

func fieldSize(payloadLen int) int {
	return 4 + payloadLen
}

func appendLengthPrefixed(buf, payload []byte) []byte {
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(payload)))
	buf = append(buf, length[:]...)
	return append(buf, payload...)
}

func readLengthPrefixed(data []byte, pos int) (payload []byte, consumed int, err error) {
	if pos+4 > len(data) {
		return nil, 0, ErrMalformedEncoding
	}
	length := int(binary.BigEndian.Uint32(data[pos : pos+4]))
	start := pos + 4
	end := start + length
	if end > len(data) || end < start {
		return nil, 0, ErrMalformedEncoding
	}
	return append([]byte(nil), data[start:end]...), 4 + length, nil
}
