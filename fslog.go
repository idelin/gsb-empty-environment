// Package fslog implements a forward-secure audit log writer and verifier.
//
// The scheme follows Schneier-Kelsey style forward integrity:
//
//   - Each epoch e uses a MAC key K_e. The next epoch key is derived
//     one-way: K_{e+1} = SHA-256("fslog/evolve" || K_e). The old key is
//     erased from memory immediately after derivation, so an attacker who
//     compromises the writer at epoch e cannot forge records for epochs < e.
//   - Every record is bound to its predecessor: mac_j = HMAC(K_e, ... ||
//     chain_{j-1} || data), and chain_j = mac_j. Deleting or reordering
//     records breaks the chain at the following record.
//   - Each epoch is closed by an epoch-end marker that binds the record
//     count and the final chain value. The next epoch's initial chain
//     incorporates the previous end marker, so whole epochs cannot be
//     removed from the middle of the log.
//
// Write protocol (needed to tell a crash apart from an attack):
//
//   - Every frame is emitted with a single Write call and is framed by a
//     length prefix AND a matching length trailer (a commit mark). A frame
//     is only valid once its trailer is present.
//   - Close (or CloseEpoch) always writes an epoch-end marker. Therefore a
//     log that ends at a clean frame boundary with an unclosed epoch has
//     been maliciously truncated; a log whose last frame is incomplete is a
//     torn tail left by a crash mid-write and is not evidence of tampering.
package fslog

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	// KeySize is the size of epoch MAC keys and chain values.
	KeySize = 32

	frameMagic   = "FSL1"
	lenSize      = 4
	headerSize   = 4 + lenSize // magic + length
	trailerSize  = lenSize
	maxFrameSize = 1 << 24 // 16 MiB sanity bound

	recTypeRecord   = 0x01
	recTypeEpochEnd = 0x02
)

var (
	labelEvolve = []byte("fslog/evolve")
	labelInit   = []byte("fslog/init")
	labelRecord = []byte("fslog/rec")
	labelEnd    = []byte("fslog/end")

	// ErrClosed is returned when appending to a closed Writer.
	ErrClosed = errors.New("fslog: writer is closed")
)

// Writer appends forward-secure, chained-MAC records to an audit log.
//
// The zero value is not usable; construct with NewWriter. A Writer is not
// safe for concurrent use.
type Writer struct {
	w     io.Writer
	sync  bool // call Sync after each frame when w is an *os.File
	key   []byte
	chain []byte
	epoch uint64
	seq   uint64 // sequence number within the current epoch
	count uint64 // records written in the current epoch

	closed    bool
	epochOpen bool // current epoch has no end marker yet
}

// Option configures a Writer.
type Option func(*Writer)

// WithSync makes the Writer call Sync after every frame when the underlying
// writer is an *os.File, so a crash can only tear the single in-flight
// frame instead of arbitrary buffered data.
func WithSync(sync bool) Option {
	return func(w *Writer) { w.sync = sync }
}

// NewWriter creates a Writer that starts at epoch 0 with the given initial
// key (KeySize bytes). The caller must keep initialKey (or be able to
// reconstruct it) to verify the log later; the Writer does not retain a
// copy beyond what it needs and erases epoch keys as it evolves.
func NewWriter(w io.Writer, initialKey []byte, opts ...Option) (*Writer, error) {
	if len(initialKey) != KeySize {
		return nil, fmt.Errorf("fslog: initial key must be %d bytes, got %d", KeySize, len(initialKey))
	}
	wr := &Writer{w: w, key: append([]byte(nil), initialKey...)}
	for _, opt := range opts {
		opt(wr)
	}
	wr.beginEpoch(nil)
	return wr, nil
}

// beginEpoch initialises the chain for the current epoch, binding it to the
// previous epoch's end marker (nil for the genesis epoch).
func (w *Writer) beginEpoch(prevEndMAC []byte) {
	mac := hmac.New(sha256.New, w.key)
	mac.Write(labelInit)
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], w.epoch)
	mac.Write(buf[:])
	mac.Write(prevEndMAC)
	w.chain = mac.Sum(nil)
	w.seq = 0
	w.count = 0
	w.epochOpen = true
}

// Append writes one audit record. The frame is emitted with a single Write
// call so a crash can leave at most one torn frame at the tail.
func (w *Writer) Append(data []byte) error {
	if w.closed {
		return ErrClosed
	}
	mac := hmac.New(sha256.New, w.key)
	mac.Write(labelRecord)
	var num [8]byte
	binary.BigEndian.PutUint64(num[:], w.epoch)
	mac.Write(num[:])
	binary.BigEndian.PutUint64(num[:], w.seq)
	mac.Write(num[:])
	mac.Write(w.chain)
	mac.Write(data)
	tag := mac.Sum(nil)

	if err := w.writeFrame(recTypeRecord, data, tag); err != nil {
		return err
	}
	w.chain = tag
	w.seq++
	w.count++
	return nil
}

// CloseEpoch writes the epoch-end marker for the current epoch, then
// derives the next epoch key one-way and erases the current key.
func (w *Writer) CloseEpoch() error {
	if w.closed {
		return ErrClosed
	}
	var cnt [8]byte
	binary.BigEndian.PutUint64(cnt[:], w.count)
	mac := hmac.New(sha256.New, w.key)
	mac.Write(labelEnd)
	var num [8]byte
	binary.BigEndian.PutUint64(num[:], w.epoch)
	mac.Write(num[:])
	mac.Write(cnt[:])
	mac.Write(w.chain)
	endTag := mac.Sum(nil)

	if err := w.writeFrame(recTypeEpochEnd, cnt[:], endTag); err != nil {
		return err
	}

	// One-way key evolution; erase the old key immediately.
	next := sha256.Sum256(append(append([]byte(nil), labelEvolve...), w.key...))
	erase(w.key)
	w.key = next[:]
	w.epoch++
	w.beginEpoch(endTag)
	return nil
}

// Close closes the current epoch (writing its end marker) and the Writer.
// The final epoch key is erased; the underlying writer is not closed.
func (w *Writer) Close() error {
	if w.closed {
		return ErrClosed
	}
	if err := w.CloseEpoch(); err != nil {
		return err
	}
	w.closed = true
	erase(w.key)
	erase(w.chain)
	return nil
}

// writeFrame emits one frame: magic || len || payload || len (trailer).
// The whole frame is handed to the underlying writer in one Write call.
func (w *Writer) writeFrame(typ byte, data, tag []byte) error {
	payloadLen := 8 + 8 + 1 + 4 + len(data) + KeySize
	if payloadLen > maxFrameSize {
		return fmt.Errorf("fslog: record too large: %d bytes", len(data))
	}
	frame := make([]byte, 0, headerSize+payloadLen+trailerSize)
	frame = append(frame, frameMagic...)
	var tmp [4]byte
	binary.BigEndian.PutUint32(tmp[:], uint32(payloadLen))
	frame = append(frame, tmp[:]...)

	var num [8]byte
	binary.BigEndian.PutUint64(num[:], w.epoch)
	frame = append(frame, num[:]...)
	binary.BigEndian.PutUint64(num[:], w.seq)
	frame = append(frame, num[:]...)
	frame = append(frame, typ)
	binary.BigEndian.PutUint32(tmp[:], uint32(len(data)))
	frame = append(frame, tmp[:]...)
	frame = append(frame, data...)
	frame = append(frame, tag...)

	binary.BigEndian.PutUint32(tmp[:], uint32(payloadLen))
	frame = append(frame, tmp[:]...)

	if _, err := w.w.Write(frame); err != nil {
		return fmt.Errorf("fslog: writing frame: %w", err)
	}
	if w.sync {
		if f, ok := w.w.(interface{ Sync() error }); ok {
			if err := f.Sync(); err != nil {
				return fmt.Errorf("fslog: syncing frame: %w", err)
			}
		}
	}
	return nil
}

// erase zeroes a sensitive buffer in place.
func erase(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

// frame is a parsed log frame.
type frame struct {
	offset int64
	epoch  uint64
	seq    uint64
	typ    byte
	data   []byte
	tag    []byte
	rawLen int64
}

// readFrames parses all complete frames from r. If the log ends with a
// partial (torn) frame, the frames parsed so far are returned along with
// the offset and size of the torn residue.
func readFrames(r io.Reader) (frames []frame, tornOffset int64, tornBytes int, err error) {
	buf, err := io.ReadAll(r)
	if err != nil {
		return nil, 0, 0, err
	}
	pos := int64(0)
	for pos < int64(len(buf)) {
		rest := buf[pos:]
		if len(rest) < headerSize {
			return frames, pos, len(rest), nil
		}
		if !bytes.Equal(rest[:4], []byte(frameMagic)) {
			return frames, pos, len(rest), nil
		}
		payloadLen := int64(binary.BigEndian.Uint32(rest[4:8]))
		if payloadLen < 8+8+1+4+KeySize || payloadLen > maxFrameSize {
			return frames, pos, len(rest), nil
		}
		total := int64(headerSize) + payloadLen + trailerSize
		if int64(len(rest)) < total {
			return frames, pos, len(rest), nil
		}
		payload := rest[headerSize : headerSize+payloadLen]
		trailer := binary.BigEndian.Uint32(rest[headerSize+payloadLen : total])
		if int64(trailer) != payloadLen {
			return frames, pos, len(rest), nil
		}
		dataLen := binary.BigEndian.Uint32(payload[17:21])
		if int64(dataLen) != payloadLen-8-8-1-4-KeySize {
			return frames, pos, len(rest), nil
		}
		frames = append(frames, frame{
			offset: pos,
			epoch:  binary.BigEndian.Uint64(payload[0:8]),
			seq:    binary.BigEndian.Uint64(payload[8:16]),
			typ:    payload[16],
			data:   append([]byte(nil), payload[21:21+dataLen]...),
			tag:    append([]byte(nil), payload[21+dataLen:]...),
			rawLen: total,
		})
		pos += total
	}
	return frames, 0, 0, nil
}
