package netsec

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"net/netip"
)

// Domain-separation labels. Fixed byte strings make every field
// self-describing inside the signed transcript, so an attacker cannot smuggle
// one field's bytes into another or splice records between prefixes/pathIDs.
const (
	domTranscript = "PathsSig/v1/transcript"
	domDigest     = "PathsSig/v1/record-digest"
	algEd25519    = "Ed25519"

	tagPrefix       = "PFX"
	tagPathID       = "PID"
	tagPreviousHash = "PRV"
	tagSignerAS     = "SIG"
	tagNextPeerAS   = "NXT"
	tagPCount       = "PCN"
	tagKeyGen       = "KGN"
	tagAlgorithm    = "ALG"
	tagSignature    = "SGN"
)

// ZeroHash is the previousHash value for the origin record (no predecessor).
var ZeroHash [32]byte

// Sentinel errors. Verification failures always return one of these wrapped,
// and a failed verification never installs anything.
var (
	ErrEmptyChain          = errors.New("netsec: signature chain has no records")
	ErrInvalidPrefix       = errors.New("netsec: invalid or non-canonical prefix")
	ErrBadOrigin           = errors.New("netsec: origin record must have signerAS == originAS and zero previousHash")
	ErrPreviousHash        = errors.New("netsec: previousHash does not match previous record digest")
	ErrHopLinkage          = errors.New("netsec: previous nextPeerAS does not equal this signerAS")
	ErrBadSignature        = errors.New("netsec: Ed25519 signature verification failed")
	ErrFinalRecipient      = errors.New("netsec: final nextPeerAS does not equal local AS")
	ErrSessionPeer         = errors.New("netsec: session peer AS does not equal final signerAS")
	ErrPCountRange         = errors.New("netsec: pCount outside [1, host maximum]")
	ErrPathTooLong         = errors.New("netsec: expanded logical path exceeds host maximum length")
	ErrASLoop              = errors.New("netsec: AS loop detected in logical path")
	ErrReservedAS          = errors.New("netsec: reserved/zero AS number used")
	ErrNoRegistry          = errors.New("netsec: host has no trusted key registry")
	ErrNoLocalAS           = errors.New("netsec: host local AS is not configured")
	ErrInvalidPrivateKey   = errors.New("netsec: invalid Ed25519 private key")
	ErrSignerMismatch      = errors.New("netsec: signing key does not correspond to signerAS")
	ErrNextPeerMismatch    = errors.New("netsec: new signerAS must equal previous nextPeerAS")
	ErrBusinessPolicy      = errors.New("netsec: business relationship policy rejected route")
	ErrPolicyNotConfigured = errors.New("netsec: no business relationship policy configured")
)

// PathID uniquely identifies one signed path within an origin/prefix context.
type PathID uint64

// HopRecord is one signed hop in the chain.
//
// Record 0 is the origin record: PreviousHash is ZeroHash and SignerAS equals
// the origin AS. Every later record's PreviousHash is the full RecordDigest of
// its predecessor, and the predecessor's NextPeerAS equals this SignerAS.
type HopRecord struct {
	SignerAS      uint32
	KeyGeneration uint64
	PreviousHash  [32]byte
	NextPeerAS    uint32
	PCount        uint32
	Signature     []byte
}

// SignedPath is the full signature chain carried by a routing update together
// with the prefix and origin metadata it is bound to.
type SignedPath struct {
	Prefix   netip.Prefix
	OriginAS uint32
	PathID   PathID
	Records  []HopRecord
}

// HopEvidence pairs a record with its full digest used for chaining.
type HopEvidence struct {
	Record HopRecord
	Digest [32]byte
}

// VerifiedPath is the cryptographic result produced on successful verification.
type VerifiedPath struct {
	Prefix         netip.Prefix
	PathID         PathID
	OriginAS       uint32
	LogicalPath    []uint32 // receiver-to-origin order (BGP AS_PATH order)
	ChainOrder     []uint32 // origin-to-receiver signer sequence
	Evidence       []HopEvidence
	KeyGenerations []uint64
}

// canonicalPrefix renders a prefix canonically: 4/16 address bytes, the
// masked address, and the bit length. Masking means only network bits are
// ever signed, eliminating host-bit ambiguity.
func canonicalPrefix(p netip.Prefix) []byte {
	addr := p.Masked().Addr()
	var addrBytes []byte
	if addr.Is4() {
		b := addr.As4()
		addrBytes = b[:]
		addrBytes = append([]byte{4}, addrBytes...)
	} else {
		b := addr.As16()
		addrBytes = append([]byte{16}, b[:]...)
	}
	return append(addrBytes, byte(p.Bits()))
}

func appendTag(b *bytes.Buffer, tag string, value []byte) {
	b.WriteString(tag)
	var lenBuf [2]byte
	lenBuf[0] = byte(len(value) >> 8)
	lenBuf[1] = byte(len(value))
	b.Write(lenBuf[:])
	b.Write(value)
}

func u32Bytes(v uint32) []byte {
	return []byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}
}

func u64Bytes(v uint64) []byte {
	return []byte{
		byte(v >> 56), byte(v >> 48), byte(v >> 40), byte(v >> 32),
		byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v),
	}
}

// recordTranscript builds the exact bytes an AS signs (and the verifier
// reconstructs). It binds the fixed domain label, prefix, pathID, previousHash,
// signerAS, nextPeerAS, pCount and keyGeneration together.
func recordTranscript(prefix netip.Prefix, pathID PathID, r HopRecord) []byte {
	var b bytes.Buffer
	b.WriteString(domTranscript)
	appendTag(&b, tagPrefix, canonicalPrefix(prefix))
	appendTag(&b, tagPathID, u64Bytes(uint64(pathID)))
	prev := r.PreviousHash[:]
	appendTag(&b, tagPreviousHash, prev)
	appendTag(&b, tagSignerAS, u32Bytes(r.SignerAS))
	appendTag(&b, tagNextPeerAS, u32Bytes(r.NextPeerAS))
	appendTag(&b, tagPCount, u32Bytes(r.PCount))
	appendTag(&b, tagKeyGen, u64Bytes(r.KeyGeneration))
	return b.Bytes()
}

// digestBytes serializes a full record (including its signature) under its own
// domain label. The result is what the next record binds via PreviousHash.
func digestBytes(prefix netip.Prefix, pathID PathID, r HopRecord) []byte {
	var b bytes.Buffer
	b.WriteString(domDigest)
	appendTag(&b, tagPrefix, canonicalPrefix(prefix))
	appendTag(&b, tagPathID, u64Bytes(uint64(pathID)))
	prev := r.PreviousHash[:]
	appendTag(&b, tagPreviousHash, prev)
	appendTag(&b, tagSignerAS, u32Bytes(r.SignerAS))
	appendTag(&b, tagNextPeerAS, u32Bytes(r.NextPeerAS))
	appendTag(&b, tagPCount, u32Bytes(r.PCount))
	appendTag(&b, tagKeyGen, u64Bytes(r.KeyGeneration))
	appendTag(&b, tagAlgorithm, []byte(algEd25519))
	appendTag(&b, tagSignature, r.Signature)
	return b.Bytes()
}

// RecordDigest returns the full digest of a record under its chain context.
func RecordDigest(prefix netip.Prefix, pathID PathID, r HopRecord) [32]byte {
	return sha256Sum(digestBytes(prefix, pathID, r))
}

func signRecord(prefix netip.Prefix, pathID PathID, r HopRecord, priv ed25519.PrivateKey) (HopRecord, error) {
	if len(priv) != ed25519.PrivateKeySize {
		return HopRecord{}, ErrInvalidPrivateKey
	}
	if r.SignerAS == 0 || r.NextPeerAS == 0 {
		return HopRecord{}, ErrReservedAS
	}
	if r.PCount == 0 {
		return HopRecord{}, ErrPCountRange
	}
	r.Signature = ed25519.Sign(priv, recordTranscript(prefix, pathID, r))
	return r, nil
}

// SignOrigin creates the origin record. It enforces signerAS == originAS and a
// zero previousHash, and binds the prefix and the intended first peerAS.
func SignOrigin(prefix netip.Prefix, originAS, targetPeerAS uint32, pathID PathID,
	pCount uint32, keyGeneration uint64, priv ed25519.PrivateKey) (HopRecord, error) {

	if originAS == 0 || targetPeerAS == 0 {
		return HopRecord{}, ErrReservedAS
	}
	prefix = prefix.Masked()
	if !prefix.IsValid() {
		return HopRecord{}, ErrInvalidPrefix
	}
	r := HopRecord{
		SignerAS:      originAS,
		KeyGeneration: keyGeneration,
		PreviousHash:  ZeroHash,
		NextPeerAS:    targetPeerAS,
		PCount:        pCount,
	}
	return signRecord(prefix, pathID, r, priv)
}

// SignHop appends a subsequent hop. The new signer must be the exact
// nextPeerAS named by the previous record; this is what binds the receiver of
// each update. The previous record's full digest becomes PreviousHash.
func SignHop(prefix netip.Prefix, pathID PathID, prev HopRecord,
	signerAS, nextPeerAS uint32, pCount uint32, keyGeneration uint64,
	priv ed25519.PrivateKey) (HopRecord, error) {

	if signerAS != prev.NextPeerAS {
		return HopRecord{}, ErrNextPeerMismatch
	}
	prefix = prefix.Masked()
	if !prefix.IsValid() {
		return HopRecord{}, ErrInvalidPrefix
	}
	r := HopRecord{
		SignerAS:      signerAS,
		KeyGeneration: uint64(keyGeneration),
		PreviousHash:  RecordDigest(prefix, pathID, prev),
		NextPeerAS:    nextPeerAS,
		PCount:        pCount,
	}
	return signRecord(prefix, pathID, r, priv)
}
