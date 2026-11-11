// Package auditlog implements an append-only, tamper-evident audit log backed
// by segmented files on disk.
//
// Every record carries a monotonic sequence number, the digest of the
// previous record, the payload length and a per-record HMAC. Segments are
// terminated by an independently signed seal. On verification, an incomplete
// frame is accepted only as a torn tail when it sits at the physical end of
// an unsealed segment. A complete frame that fails authentication is always
// reported as tampering; the package never truncates or overwrites evidence.
package auditlog

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	recordMagic byte = 0xA1
	sealMagic   byte = 0xA2

	version1 byte = 1
	flagNone byte = 0

	digestLen = sha256.Size
	hmacLen   = sha256.Size
	sigLen    = ed25519.SignatureSize

	// magic(1) + version(1) + flags(1) + seq(8) + payloadLen(4) +
	// prevDigest(32) + hmac(32)
	recordHeaderLen = 3 + 8 + 4 + digestLen + hmacLen

	// magic(1) + version(1) + flags(1) + segmentIndex(8) + recordCount(8) +
	// firstSeq(8) + lastSeq(8) + prevSealDigest(32) + lastRecordDigest(32)
	sealHeaderLen = 3 + 4*8 + 2*digestLen
	sealFrameLen  = sealHeaderLen + sigLen

	// DefaultMaxSegmentRecords is used when WithMaxSegmentRecords is not set.
	DefaultMaxSegmentRecords = 4096
)

// RecordHeaderLen is the on-disk size of a record frame header, excluding the
// payload.
const RecordHeaderLen = recordHeaderLen

// SealHeaderLen and SealFrameLen are the on-disk sizes of the signed seal
// header and of the complete seal frame.
const (
	SealHeaderLen = sealHeaderLen
	SealFrameLen  = sealFrameLen
)

var (
	// genesisDigest anchors the first record and the first seal so that an
	// all-zero memory block cannot be mistaken for a legitimate predecessor.
	genesisDigest = sha256.Sum256(nil)

	errCleanEOF = errors.New("auditlog: clean end of segment")
	errTorn     = errors.New("auditlog: torn segment tail")
)

// Keys holds the cryptographic material used by writers and readers.
type Keys struct {
	// HMAC authenticates every record frame.
	HMAC []byte
	// Sign signs segment seals and must correspond to Verify.
	Sign ed25519.PrivateKey
	// Verify verifies segment seals.
	Verify ed25519.PublicKey
}

// GenerateKeys returns a fresh key set using crypto/rand.
func GenerateKeys() (Keys, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return Keys{}, err
	}
	hmacKey := make([]byte, digestLen)
	if _, err := rand.Read(hmacKey); err != nil {
		return Keys{}, err
	}
	return Keys{HMAC: hmacKey, Sign: priv, Verify: pub}, nil
}

func (k Keys) validate(forWriting bool) error {
	if len(k.HMAC) == 0 {
		return errors.New("auditlog: HMAC key is required")
	}
	if len(k.Verify) != ed25519.PublicKeySize {
		return errors.New("auditlog: ed25519 public key is required")
	}
	if forWriting {
		if len(k.Sign) != ed25519.PrivateKeySize {
			return errors.New("auditlog: ed25519 private key is required")
		}
		pub, ok := k.Sign.Public().(ed25519.PublicKey)
		if !ok || !pub.Equal(k.Verify) {
			return errors.New("auditlog: signing and verifying keys do not match")
		}
	}
	return nil
}

// TamperKind classifies a detected integrity violation.
type TamperKind string

const (
	// TamperHMAC means a complete record frame failed HMAC authentication.
	TamperHMAC TamperKind = "hmac_authentication_failed"
	// TamperChain means a sequence number or predecessor digest is inconsistent.
	TamperChain TamperKind = "hash_chain_broken"
	// TamperSeal means a segment seal is missing, invalid or has trailing data.
	TamperSeal TamperKind = "seal_verification_failed"
	// TamperFrame means framing bytes are invalid or a sealed segment is truncated.
	TamperFrame TamperKind = "invalid_frame"
	// TamperOrder means segment file names, ordering or extension state is invalid.
	TamperOrder TamperKind = "segment_order_invalid"
)

// TamperError describes a concrete integrity violation. It is returned instead
// of silently dropping or truncating the offending data.
type TamperError struct {
	Kind     TamperKind
	Segment  uint64
	Sequence uint64
	File     string
	Offset   int64
	Detail   string
}

func (e *TamperError) Error() string {
	return fmt.Sprintf("auditlog: tampering detected (%s) segment=%d sequence=%d file=%q offset=%d: %s",
		e.Kind, e.Segment, e.Sequence, e.File, e.Offset, e.Detail)
}

func tamper(kind TamperKind, segment, sequence uint64, file string, offset int64, detail string) *TamperError {
	return &TamperError{
		Kind:     kind,
		Segment:  segment,
		Sequence: sequence,
		File:     file,
		Offset:   offset,
		Detail:   detail,
	}
}

// Record is one authenticated payload returned by ReadDir.
type Record struct {
	Segment  uint64
	Sequence uint64
	Payload  []byte
}

// TornTail describes an incomplete frame left at the physical end of an
// unsealed segment, typically by a power loss mid-write.
type TornTail struct {
	Segment uint64
	File    string
	Bytes   int64
}

// SegmentStatus summarizes one parsed segment file.
type SegmentStatus struct {
	Index         uint64
	File          string
	Sealed        bool
	Records       int
	FirstSequence uint64
	LastSequence  uint64
	TornTailBytes int64
	// SealDigest is the digest of the full seal frame; zero for unsealed
	// segments.
	SealDigest []byte
	// LastRecordDigest is the digest of the final record frame; zero for
	// segments without records.
	LastRecordDigest []byte
}

// Report is the verified result of scanning a log directory.
type Report struct {
	Segments     []SegmentStatus
	Records      []Record
	TornTails    []TornTail
	LastSequence uint64
	// LastRecordDigest is the digest of the final authenticated record across
	// the whole log; zero when the log contains no records.
	LastRecordDigest []byte
}

func encodeRecord(seq uint64, prevDigest, payload, hmacKey []byte) []byte {
	frame := make([]byte, recordHeaderLen+len(payload))
	frame[0] = recordMagic
	frame[1] = version1
	frame[2] = flagNone
	binary.BigEndian.PutUint64(frame[3:11], seq)
	binary.BigEndian.PutUint32(frame[11:15], uint32(len(payload)))
	copy(frame[15:15+digestLen], prevDigest)

	mac := hmac.New(sha256.New, hmacKey)
	mac.Write(frame[3:15])
	mac.Write(prevDigest)
	mac.Write(payload)
	copy(frame[15+digestLen:15+digestLen+hmacLen], mac.Sum(nil))
	copy(frame[recordHeaderLen:], payload)
	return frame
}

func recordDigest(frame []byte) []byte {
	sum := sha256.Sum256(frame)
	return sum[:]
}

func encodeSeal(index, count, firstSeq, lastSeq uint64, prevSealDigest, lastRecordDigest []byte, signKey ed25519.PrivateKey) []byte {
	frame := make([]byte, sealFrameLen)
	frame[0] = sealMagic
	frame[1] = version1
	frame[2] = flagNone
	binary.BigEndian.PutUint64(frame[3:11], index)
	binary.BigEndian.PutUint64(frame[11:19], count)
	binary.BigEndian.PutUint64(frame[19:27], firstSeq)
	binary.BigEndian.PutUint64(frame[27:35], lastSeq)
	copy(frame[35:35+digestLen], prevSealDigest)
	copy(frame[35+digestLen:35+2*digestLen], lastRecordDigest)
	sig := ed25519.Sign(signKey, frame[:sealHeaderLen])
	copy(frame[sealHeaderLen:], sig)
	return frame
}

func sealDigest(frame []byte) []byte {
	sum := sha256.Sum256(frame)
	return sum[:]
}
