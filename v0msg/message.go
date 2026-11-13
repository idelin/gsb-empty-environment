// Package v0msg parses Solana v0 (versioned) transaction messages and
// enforces pre-signing policy checks for wallets.
//
// Account ordering and writability follow the Solana convention: static
// account keys come first, then writable accounts loaded from address
// lookup tables, then readonly loaded accounts. Lookup table contents are
// injected by the caller; this package never resolves tables on its own.
package v0msg

import (
	"errors"
	"fmt"
)

// VersionPrefix is the required first byte of a v0 message.
const VersionPrefix = 0x80

// MaxAccounts is the hard limit on the total number of accounts
// (static + loaded) a message may reference.
const MaxAccounts = 256

var (
	// ErrBadVersion is returned when the first byte is not exactly 0x80.
	// This rejects legacy (unversioned) messages (< 0x80) and unknown
	// future versions (>= 0x81) alike.
	ErrBadVersion = errors.New("v0msg: message is not a v0 message (first byte must be 0x80)")
	// ErrTruncated is returned when the message ends prematurely.
	ErrTruncated = errors.New("v0msg: truncated message")
	// ErrTrailingData is returned when bytes remain after the message.
	ErrTrailingData = errors.New("v0msg: trailing bytes after message")
)

// Header holds the three message header counts.
type Header struct {
	NumRequiredSignatures       uint8
	NumReadonlySignedAccounts   uint8
	NumReadonlyUnsignedAccounts uint8
}

// Instruction is a compiled instruction as it appears in the message.
type Instruction struct {
	ProgramIDIndex uint8
	AccountIndexes []uint8
	Data           []byte
}

// TableLookupRef is an on-chain address lookup table reference: the table
// address plus the indexes to load as writable and as readonly.
type TableLookupRef struct {
	TableAddress    [32]byte
	WritableIndexes []uint8
	ReadonlyIndexes []uint8
}

// Message is a parsed v0 transaction message.
type Message struct {
	Header       Header
	StaticKeys   [][32]byte
	RecentBlock  [32]byte
	Instructions []Instruction
	Lookups      []TableLookupRef
}

type reader struct {
	buf []byte
	off int
}

func (r *reader) byte() (byte, error) {
	if r.off >= len(r.buf) {
		return 0, ErrTruncated
	}
	b := r.buf[r.off]
	r.off++
	return b, nil
}

func (r *reader) take(n int) ([]byte, error) {
	if n < 0 || len(r.buf)-r.off < n {
		return nil, ErrTruncated
	}
	b := r.buf[r.off : r.off+n]
	r.off += n
	return b, nil
}

// shortvec decodes a Solana shortvec (compact-u16) length.
func (r *reader) shortvec() (int, error) {
	var (
		val   int
		shift uint
	)
	for i := 0; i < 3; i++ {
		b, err := r.byte()
		if err != nil {
			return 0, err
		}
		val |= int(b&0x7f) << shift
		if b&0x80 == 0 {
			return val, nil
		}
		shift += 7
	}
	return 0, fmt.Errorf("v0msg: shortvec length exceeds 16 bits")
}

func (r *reader) key() ([32]byte, error) {
	var k [32]byte
	b, err := r.take(32)
	if err != nil {
		return k, err
	}
	copy(k[:], b)
	return k, nil
}

// ParseMessage decodes a raw v0 message. It only validates structure and
// the version prefix; policy checks live in Inspect.
func ParseMessage(raw []byte) (*Message, error) {
	r := &reader{buf: raw}

	prefix, err := r.byte()
	if err != nil {
		return nil, err
	}
	if prefix != VersionPrefix {
		return nil, fmt.Errorf("%w: got 0x%02x", ErrBadVersion, prefix)
	}

	var msg Message
	hdr, err := r.take(3)
	if err != nil {
		return nil, err
	}
	msg.Header = Header{
		NumRequiredSignatures:       hdr[0],
		NumReadonlySignedAccounts:   hdr[1],
		NumReadonlyUnsignedAccounts: hdr[2],
	}

	nKeys, err := r.shortvec()
	if err != nil {
		return nil, err
	}
	msg.StaticKeys = make([][32]byte, 0, nKeys)
	for i := 0; i < nKeys; i++ {
		k, err := r.key()
		if err != nil {
			return nil, err
		}
		msg.StaticKeys = append(msg.StaticKeys, k)
	}

	msg.RecentBlock, err = r.key()
	if err != nil {
		return nil, err
	}

	nIx, err := r.shortvec()
	if err != nil {
		return nil, err
	}
	msg.Instructions = make([]Instruction, 0, nIx)
	for i := 0; i < nIx; i++ {
		var ix Instruction
		prog, err := r.byte()
		if err != nil {
			return nil, err
		}
		ix.ProgramIDIndex = prog

		nAcct, err := r.shortvec()
		if err != nil {
			return nil, err
		}
		accts, err := r.take(nAcct)
		if err != nil {
			return nil, err
		}
		ix.AccountIndexes = append([]uint8(nil), accts...)

		nData, err := r.shortvec()
		if err != nil {
			return nil, err
		}
		data, err := r.take(nData)
		if err != nil {
			return nil, err
		}
		ix.Data = append([]byte(nil), data...)

		msg.Instructions = append(msg.Instructions, ix)
	}

	nLk, err := r.shortvec()
	if err != nil {
		return nil, err
	}
	msg.Lookups = make([]TableLookupRef, 0, nLk)
	for i := 0; i < nLk; i++ {
		var lk TableLookupRef
		lk.TableAddress, err = r.key()
		if err != nil {
			return nil, err
		}
		nW, err := r.shortvec()
		if err != nil {
			return nil, err
		}
		w, err := r.take(nW)
		if err != nil {
			return nil, err
		}
		lk.WritableIndexes = append([]uint8(nil), w...)
		nR, err := r.shortvec()
		if err != nil {
			return nil, err
		}
		ro, err := r.take(nR)
		if err != nil {
			return nil, err
		}
		lk.ReadonlyIndexes = append([]uint8(nil), ro...)
		msg.Lookups = append(msg.Lookups, lk)
	}

	if r.off != len(r.buf) {
		return nil, fmt.Errorf("%w: %d bytes", ErrTrailingData, len(r.buf)-r.off)
	}
	return &msg, nil
}
