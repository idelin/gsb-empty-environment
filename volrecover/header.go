package volrecover

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sort"
)

// Header is one signed volume metadata header. Two generations may coexist
// on the same image during an upgrade.
type Header struct {
	// Epoch is the generation of this header.
	Epoch Epoch
	// Parent is the digest of the predecessor header. The genesis header uses
	// the zero digest.
	Parent Digest
	// Slots declares each key slot range together with exact, versioned unit
	// commitments.
	Slots []SlotSpec
	// Segments declares the encrypted segment ranges.
	Segments []Interval
	// Grants is the signed slot-to-segment authorization table.
	Grants []Grant
	// ID is the digest of the canonical header body. It must be recomputed
	// with ComputeID before signing.
	ID Digest
	// Sig is the Ed25519 signature over ID under the trust root key.
	Sig []byte
}

// TrustRoot is the host-selected current root of trust. Revoked holds the
// durable, monotonically increasing revocation register: slot id mapped to
// the revocation sequence at which it was revoked.
type TrustRoot struct {
	RootKey ed25519.PublicKey
	Root    Digest
	Revoked map[SlotID]uint64
}

// NewTrustRoot builds a trust root from the root verification key, the
// currently committed root pointer, and the durable revocation register.
func NewTrustRoot(key ed25519.PublicKey, rootPointer Digest, revoked map[SlotID]uint64) *TrustRoot {
	copyRevoked := make(map[SlotID]uint64, len(revoked))
	for slot, seq := range revoked {
		copyRevoked[slot] = seq
	}
	return &TrustRoot{
		RootKey: append(ed25519.PublicKey(nil), key...),
		Root:    rootPointer,
		Revoked: copyRevoked,
	}
}

// IsRevoked reports whether the slot is present in the monotonic revocation
// register. A revocation can never be withdrawn.
func (r *TrustRoot) IsRevoked(slot SlotID) bool {
	_, ok := r.Revoked[slot]
	return ok
}

func putU64(b *bytes.Buffer, v uint64) {
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], v)
	b.Write(buf[:])
}

func putU32(b *bytes.Buffer, v uint32) {
	var buf [4]byte
	binary.BigEndian.PutUint32(buf[:], v)
	b.Write(buf[:])
}

func putDigest(b *bytes.Buffer, d Digest) { b.Write(d[:]) }

func putInterval(b *bytes.Buffer, i Interval) {
	putU64(b, i.Start)
	putU64(b, i.End)
}

func putUnitRef(b *bytes.Buffer, ref UnitRef) {
	putU64(b, uint64(ref.Unit))
	putU64(b, ref.Version)
	putDigest(b, ref.Commit)
}

func putSlot(b *bytes.Buffer, slot SlotSpec) {
	putU32(b, uint32(slot.ID))
	putInterval(b, slot.Range)
	putU64(b, uint64(len(slot.Units)))
	for _, ref := range slot.Units {
		putUnitRef(b, ref)
	}
}

func putGrant(b *bytes.Buffer, grant Grant) {
	putU32(b, uint32(grant.Slot))
	putU32(b, uint32(grant.Segment))
}

// encodeHeaderBody renders the signable header body deterministically. Slices
// are sorted here so signatures are independent of in-memory ordering.
func encodeHeaderBody(h *Header) []byte {
	slots := append([]SlotSpec(nil), h.Slots...)
	sort.Slice(slots, func(i, j int) bool { return slots[i].ID < slots[j].ID })
	for i := range slots {
		sort.Slice(slots[i].Units, func(a, b int) bool {
			if slots[i].Units[a].Unit != slots[i].Units[b].Unit {
				return slots[i].Units[a].Unit < slots[i].Units[b].Unit
			}
			return slots[i].Units[a].Version < slots[i].Units[b].Version
		})
	}
	segments := append([]Interval(nil), h.Segments...)
	sort.Slice(segments, func(i, j int) bool {
		if segments[i].Start != segments[j].Start {
			return segments[i].Start < segments[j].Start
		}
		return segments[i].End < segments[j].End
	})
	grants := append([]Grant(nil), h.Grants...)
	sort.Slice(grants, func(i, j int) bool {
		if grants[i].Slot != grants[j].Slot {
			return grants[i].Slot < grants[j].Slot
		}
		return grants[i].Segment < grants[j].Segment
	})

	var b bytes.Buffer
	b.WriteString("volrecover-header/v1\n")
	putU64(&b, uint64(h.Epoch))
	putDigest(&b, h.Parent)

	putU64(&b, uint64(len(slots)))
	for _, slot := range slots {
		putSlot(&b, slot)
	}
	putU64(&b, uint64(len(segments)))
	for _, seg := range segments {
		putInterval(&b, seg)
	}
	putU64(&b, uint64(len(grants)))
	for _, grant := range grants {
		putGrant(&b, grant)
	}
	return b.Bytes()
}

// ComputeID returns the digest of the canonical header body and stores it in
// the header.
func ComputeID(h *Header) Digest {
	h.ID = sha256.Sum256(encodeHeaderBody(h))
	return h.ID
}

// SignHeader recomputes the header id and signs it with the supplied Ed25519
// private key.
func SignHeader(h *Header, signKey ed25519.PrivateKey) Digest {
	id := ComputeID(h)
	h.Sig = append([]byte(nil), ed25519.Sign(signKey, id[:])...)
	return id
}

// VerifyHeader checks the stored id against the canonical body and verifies
// the Ed25519 signature under the trust root.
func VerifyHeader(h *Header, root *TrustRoot) Reason {
	if h == nil {
		return Reason{Code: ReasonMalformedHeader, Detail: "nil header"}
	}
	want := sha256.Sum256(encodeHeaderBody(h))
	if want != h.ID {
		return Reason{Code: ReasonMalformedHeader, Detail: "header id does not match canonical body", Witness: shortID(h.ID)}
	}
	if len(h.Sig) != ed25519.SignatureSize || !ed25519.Verify(root.RootKey, h.ID[:], h.Sig) {
		return Reason{Code: ReasonBadSignature, Witness: shortID(h.ID)}
	}
	return Reason{}
}

// UnitCommit computes the content commitment for a unit payload.
func UnitCommit(payload []byte) Digest {
	h := sha256.New()
	h.Write([]byte("volrecover-unit/v1\n"))
	h.Write(payload)
	var out Digest
	copy(out[:], h.Sum(nil))
	return out
}

// GenerateRootKey is a convenience for hosts and tests that need a fresh
// Ed25519 root key pair.
func GenerateRootKey() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	return ed25519.GenerateKey(rand.Reader)
}

func shortID(id Digest) string {
	return fmt.Sprintf("%x", id[:8])
}
