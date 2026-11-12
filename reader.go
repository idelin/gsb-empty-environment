package netsec

import (
	"errors"
	"fmt"
	"io"
)

// DefaultMaxBodyBytes is the default upper bound on the authenticated body
// length a single frame may declare. The bound is intentionally modest so that
// an attacker-controlled length field can never force a large allocation
// before that length has itself been authenticated.
const DefaultMaxBodyBytes = 256 * 1024

// Sentinel errors returned by Reader.ReadPacket. Callers should use errors.Is
// rather than comparing messages.
var (
	// ErrDeclaredLength is returned when the authenticated header advertises a
	// body length outside of the configured bound. No body buffer is allocated.
	ErrDeclaredLength = errors.New("netsec: declared body length exceeds limit")
	// ErrAlignment is returned when header plus body size is not a multiple of
	// the cipher block size. No body buffer is allocated.
	ErrAlignment = errors.New("netsec: frame is not aligned to cipher block size")
	// ErrHeaderAuth wraps the transform failure when the protected header does
	// not authenticate.
	ErrHeaderAuth = errors.New("netsec: protected header authentication failed")
	// ErrBodyAuth wraps the transform failure when the protected body does not
	// authenticate. The freshly read body buffer is discarded.
	ErrBodyAuth = errors.New("netsec: body authentication failed")
)

// FrameTransform is the injected authenticated-cipher boundary. Implementations
// own all cryptographic details (keys, nonces, MAC/AEAD tags); the Reader only
// owns framing, bounds, and buffer lifecycle.
//
// The on-wire frame layout handled by the Reader is:
//
//	fixed-size protected header  (HeaderSize bytes, carries the body length)
//	protected body              (length bytes, length taken from the header)
type FrameTransform interface {
	// HeaderSize is the fixed on-wire length of every protected header.
	HeaderSize() int

	// BlockSize is the cipher block size. A frame is well formed only when
	// HeaderSize plus body length is a multiple of it. Returning 1 (or less)
	// disables alignment checks for stream constructions.
	BlockSize() int

	// OpenHeader authenticates and decrypts a protected header read from the
	// wire and returns the declared body length. It must not trust any field
	// before authentication succeeds.
	OpenHeader(protected []byte) (length uint32, err error)

	// OpenBody authenticates and decrypts the protected body, writing the
	// plaintext into dst. The protected header is supplied as associated data
	// for encrypt-then-MAC style constructions. dst is allowed to alias
	// protected so implementations may decrypt in place; on error the contents
	// of dst are irrelevant because the Reader discards the buffer.
	OpenBody(dst, header, protected []byte) error
}

// Option configures a Reader at construction.
type Option func(*Reader)

// WithMaxBody overrides the maximum accepted authenticated body length.
// Negative values are ignored.
func WithMaxBody(max int) Option {
	return func(rd *Reader) {
		if max >= 0 {
			rd.maxBody = max
		}
	}
}

// Reader consumes authenticated frames from an underlying stream. It keeps a
// single reusable fixed-size header buffer and never retains a delivered body
// buffer, so failed frames cannot cause an old packet to be redelivered.
type Reader struct {
	r       io.Reader
	tr      FrameTransform
	maxBody int
	header  []byte
}

// NewReader returns a Reader pulling frames from r through tr. The small header
// buffer is allocated once here and reused for every frame.
func NewReader(r io.Reader, tr FrameTransform, opts ...Option) *Reader {
	if r == nil {
		panic("netsec: nil underlying reader")
	}
	if tr == nil {
		panic("netsec: nil frame transform")
	}
	rd := &Reader{
		r:       r,
		tr:      tr,
		maxBody: DefaultMaxBodyBytes,
	}
	for _, opt := range opts {
		opt(rd)
	}
	if size := tr.HeaderSize(); size > 0 {
		rd.header = make([]byte, size)
	}
	return rd
}

// ReadPacket reads one frame in gated stages and returns the decrypted body
// only after both the header and the body have authenticated. On any error it
// returns a nil packet and drops any partially read buffer; the reusable
// header buffer is the only storage it keeps.
func (rd *Reader) ReadPacket() (body []byte, err error) {
	if len(rd.header) == 0 {
		return nil, errors.New("netsec: transform reports non-positive header size")
	}

	// Stage 1: read the fixed, small protected header. ReadFull never grows the
	// buffer to match a sender-controlled length.
	if _, err = io.ReadFull(rd.r, rd.header); err != nil {
		return nil, err
	}

	// Stage 2: authenticate the header before trusting its length field.
	length, err := rd.tr.OpenHeader(rd.header)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrHeaderAuth, err)
	}

	// Stage 3: validate the declared length and block alignment while still
	// holding only the tiny header buffer. Bare sentinels keep this rejection
	// path allocation free.
	n := int(length)
	if n < 0 || n > rd.maxBody {
		return nil, ErrDeclaredLength
	}
	if block := rd.tr.BlockSize(); block > 1 && (len(rd.header)+n)%block != 0 {
		return nil, ErrAlignment
	}

	// Stage 4: only now allocate a body buffer, bounded by maxBody, and read
	// exactly the declared number of protected bytes.
	body = make([]byte, n)
	if _, err = io.ReadFull(rd.r, body); err != nil {
		// Truncated body: discard the partial buffer and deliver nothing.
		return nil, err
	}

	// Stage 5: authenticate and decrypt in place. On failure the buffer is
	// dropped rather than cached, so it can never be redelivered later.
	if err = rd.tr.OpenBody(body, rd.header, body); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBodyAuth, err)
	}
	return body, nil
}
