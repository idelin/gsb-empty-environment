package taskpkg

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	recordDomain = "example.org/domainreissue/q011/record/v1\x00"
	sealDomain   = "example.org/domainreissue/q011/seal/v1\x00"
)

// Digest is a SHA-256 digest or a hash-chain link.
type Digest [sha256.Size]byte

// ZeroDigest is the previous digest of the first record in a chain.
var ZeroDigest Digest

// IsZero reports whether d is the all-zero digest.
func (d Digest) IsZero() bool {
	return d == ZeroDigest
}

// String returns the digest in lowercase hexadecimal form.
func (d Digest) String() string {
	return fmt.Sprintf("%x", d[:])
}

// HashPayload returns the content digest used for a record payload.
func HashPayload(payload []byte) Digest {
	return Digest(sha256.Sum256(payload))
}

// Record is one signed link in a per-device, per-boot hash chain.
//
// Device, Boot, Seq, Prev, and Payload are covered by Sig. Link is the hash
// of those fields and becomes the Prev value of the next record.
type Record struct {
	Device  ed25519.PublicKey `json:"device"`
	Boot    []byte            `json:"boot"`
	Seq     uint64            `json:"seq"`
	Prev    Digest            `json:"prev"`
	Payload []byte            `json:"payload,omitempty"`
	Sig     []byte            `json:"sig"`
}

// Entry is an alias for Record.
type Entry = Record

// Message returns the canonical bytes covered by a record signature.
func (r Record) Message() []byte {
	buf := make([]byte, 0, len(recordDomain)+len(r.Device)+len(r.Boot)+len(r.Payload)+64)
	buf = append(buf, recordDomain...)
	buf = appendFixedLength(buf, r.Device)
	buf = appendFixedLength(buf, r.Boot)
	buf = binary.BigEndian.AppendUint64(buf, r.Seq)
	buf = append(buf, r.Prev[:]...)
	buf = appendFixedLength(buf, r.Payload)
	return buf
}

// Link returns the hash-chain digest of the record.
func (r Record) Link() Digest {
	return Digest(sha256.Sum256(r.Message()))
}

// Verify checks the record shape and its Ed25519 signature.
func (r Record) Verify() error {
	if len(r.Device) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: device key has length %d, want %d",
			ErrInvalidRecord, len(r.Device), ed25519.PublicKeySize)
	}
	if len(r.Sig) != ed25519.SignatureSize {
		return fmt.Errorf("%w: signature has length %d, want %d",
			ErrInvalidRecord, len(r.Sig), ed25519.SignatureSize)
	}
	if !ed25519.Verify(r.Device, r.Message(), r.Sig) {
		return fmt.Errorf("%w: Ed25519 signature does not verify", ErrInvalidRecord)
	}
	return nil
}

// Seal attests to the final sequence number and root digest of a chain.
type Seal struct {
	Device ed25519.PublicKey `json:"device"`
	Boot   []byte            `json:"boot"`
	Seq    uint64            `json:"seq"`
	Root   Digest            `json:"root"`
	Sig    []byte            `json:"sig"`
}

// Message returns the canonical bytes covered by a seal signature.
func (s Seal) Message() []byte {
	buf := make([]byte, 0, len(sealDomain)+len(s.Device)+len(s.Boot)+len(s.Root)+64)
	buf = append(buf, sealDomain...)
	buf = appendFixedLength(buf, s.Device)
	buf = appendFixedLength(buf, s.Boot)
	buf = binary.BigEndian.AppendUint64(buf, s.Seq)
	buf = append(buf, s.Root[:]...)
	return buf
}

// Verify checks the seal shape and its Ed25519 signature. It does not by
// itself prove that the seal belongs to any particular chain.
func (s Seal) Verify() error {
	if len(s.Device) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: device key has length %d, want %d",
			ErrInvalidSeal, len(s.Device), ed25519.PublicKeySize)
	}
	if len(s.Sig) != ed25519.SignatureSize {
		return fmt.Errorf("%w: signature has length %d, want %d",
			ErrInvalidSeal, len(s.Sig), ed25519.SignatureSize)
	}
	if !ed25519.Verify(s.Device, s.Message(), s.Sig) {
		return fmt.Errorf("%w: Ed25519 signature does not verify", ErrInvalidSeal)
	}
	return nil
}

// SignRecord signs one record with priv. The device public key is derived
// from priv. prev must be ZeroDigest for the first record of a chain.
func SignRecord(priv ed25519.PrivateKey, boot []byte, seq uint64, prev Digest, payload []byte) (Record, error) {
	if len(priv) != ed25519.PrivateKeySize {
		return Record{}, fmt.Errorf("%w: private key has length %d, want %d",
			ErrInvalidRecord, len(priv), ed25519.PrivateKeySize)
	}
	pub, ok := priv.Public().(ed25519.PublicKey)
	if !ok || len(pub) != ed25519.PublicKeySize {
		return Record{}, fmt.Errorf("%w: private key does not yield an Ed25519 public key", ErrInvalidRecord)
	}
	r := Record{
		Device:  append(ed25519.PublicKey(nil), pub...),
		Boot:    append([]byte(nil), boot...),
		Seq:     seq,
		Prev:    prev,
		Payload: append([]byte(nil), payload...),
	}
	r.Sig = ed25519.Sign(priv, r.Message())
	return r, nil
}

// SignSeal signs the final sequence number and root digest of a chain.
func SignSeal(priv ed25519.PrivateKey, boot []byte, seq uint64, root Digest) (Seal, error) {
	if len(priv) != ed25519.PrivateKeySize {
		return Seal{}, fmt.Errorf("%w: private key has length %d, want %d",
			ErrInvalidSeal, len(priv), ed25519.PrivateKeySize)
	}
	pub, ok := priv.Public().(ed25519.PublicKey)
	if !ok || len(pub) != ed25519.PublicKeySize {
		return Seal{}, fmt.Errorf("%w: private key does not yield an Ed25519 public key", ErrInvalidSeal)
	}
	s := Seal{
		Device: append(ed25519.PublicKey(nil), pub...),
		Boot:   append([]byte(nil), boot...),
		Seq:    seq,
		Root:   root,
	}
	s.Sig = ed25519.Sign(priv, s.Message())
	return s, nil
}

// Recorder appends records to one device/boot hash chain.
type Recorder struct {
	priv   ed25519.PrivateKey
	boot   []byte
	next   uint64
	prev   Digest
	last   Record
	count  int
	sealed bool
}

// NewRecorder creates a chain whose first record has sequence number zero.
func NewRecorder(priv ed25519.PrivateKey, boot []byte) (*Recorder, error) {
	return NewRecorderAt(priv, boot, 0)
}

// NewRecorderFromSeed creates a chain from a 32-byte Ed25519 seed. Its first
// record has sequence number zero.
func NewRecorderFromSeed(seed, boot []byte) (*Recorder, error) {
	if len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("%w: seed has length %d, want %d",
			ErrInvalidRecord, len(seed), ed25519.SeedSize)
	}
	return NewRecorder(ed25519.NewKeyFromSeed(seed), boot)
}

// NewRecorderAt creates a chain whose first record has sequence number start.
func NewRecorderAt(priv ed25519.PrivateKey, boot []byte, start uint64) (*Recorder, error) {
	if len(priv) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("%w: private key has length %d, want %d",
			ErrInvalidRecord, len(priv), ed25519.PrivateKeySize)
	}
	if _, ok := priv.Public().(ed25519.PublicKey); !ok {
		return nil, fmt.Errorf("%w: private key does not yield an Ed25519 public key", ErrInvalidRecord)
	}
	return &Recorder{
		priv: append(ed25519.PrivateKey(nil), priv...),
		boot: append([]byte(nil), boot...),
		next: start,
	}, nil
}

// Append signs and appends payload. Records must be appended in sequence
// order; verification of unordered collections is handled separately.
func (r *Recorder) Append(payload []byte) (Record, error) {
	if r == nil {
		return Record{}, fmt.Errorf("%w: nil recorder", ErrInvalidRecord)
	}
	if r.sealed {
		return Record{}, fmt.Errorf("%w: chain is already sealed", ErrInvalidRecord)
	}
	rec, err := SignRecord(r.priv, r.boot, r.next, r.prev, payload)
	if err != nil {
		return Record{}, err
	}
	r.last = rec
	r.prev = rec.Link()
	r.next++
	r.count++
	return rec, nil
}

// NextSeq returns the sequence number that Append will use next.
func (r *Recorder) NextSeq() uint64 {
	if r == nil {
		return 0
	}
	return r.next
}

// Last returns the most recently appended record.
func (r *Recorder) Last() (Record, bool) {
	if r == nil || r.count == 0 {
		return Record{}, false
	}
	return r.last, true
}

// Seal signs the current final sequence number and root digest.
func (r *Recorder) Seal() (Seal, error) {
	if r == nil {
		return Seal{}, fmt.Errorf("%w: nil recorder", ErrInvalidSeal)
	}
	if r.count == 0 {
		return Seal{}, fmt.Errorf("%w: cannot seal an empty chain", ErrInvalidSeal)
	}
	s, err := SignSeal(r.priv, r.boot, r.last.Seq, r.prev)
	if err != nil {
		return Seal{}, err
	}
	r.sealed = true
	return s, nil
}

// Clone returns an independent copy of the recorder. It is useful for
// constructing deliberate forks in tests.
func (r *Recorder) Clone() *Recorder {
	if r == nil {
		return nil
	}
	return &Recorder{
		priv:   append(ed25519.PrivateKey(nil), r.priv...),
		boot:   append([]byte(nil), r.boot...),
		next:   r.next,
		prev:   r.prev,
		last:   cloneRecord(r.last),
		count:  r.count,
		sealed: r.sealed,
	}
}

func cloneRecord(r Record) Record {
	r.Device = append(ed25519.PublicKey(nil), r.Device...)
	r.Boot = append([]byte(nil), r.Boot...)
	r.Payload = append([]byte(nil), r.Payload...)
	r.Sig = append([]byte(nil), r.Sig...)
	return r
}

func appendFixedLength(dst, b []byte) []byte {
	dst = binary.BigEndian.AppendUint32(dst, uint32(len(b)))
	return append(dst, b...)
}

var (
	// ErrInvalidRecord identifies a malformed record or bad record signature.
	ErrInvalidRecord = errors.New("taskpkg: invalid record")
	// ErrInvalidSeal identifies a malformed seal or bad seal signature.
	ErrInvalidSeal = errors.New("taskpkg: invalid seal")
	// ErrEmpty identifies an empty record set.
	ErrEmpty = errors.New("taskpkg: no records")
	// ErrMixedContext identifies records from more than one device or boot.
	ErrMixedContext = errors.New("taskpkg: records span multiple device/boot contexts")
	// ErrFork identifies two or more valid signed records at one sequence.
	ErrFork = errors.New("taskpkg: fork detected")
	// ErrIncomplete identifies a missing sequence, broken link, absent genesis,
	// or a seal beyond the usable chain.
	ErrIncomplete = errors.New("taskpkg: chain is incomplete")
	// ErrSealContext identifies a seal for a different device or boot.
	ErrSealContext = errors.New("taskpkg: seal belongs to a different device/boot")
	// ErrSealMismatch identifies a seal whose sequence or root is wrong.
	ErrSealMismatch = errors.New("taskpkg: seal does not match chain endpoint")
)
