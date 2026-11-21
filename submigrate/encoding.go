package submigrate

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
)

// Digest is a SHA-256 content digest.
type Digest [sha256.Size]byte

// All canonical byte strings use fixed-width, length-prefixed fields with a
// leading domain tag, so signatures made for one structure can never be
// replayed as a valid signature for another.

func putUvar64(dst []byte, v uint64) []byte {
	var buf [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(buf[:], v)
	dst = append(dst, byte(n))
	dst = append(dst, buf[:n]...)
	return dst
}

func putBytes(dst, b []byte) []byte {
	dst = putUvar64(dst, uint64(len(b)))
	return append(dst, b...)
}

func putString(dst []byte, s string) []byte {
	return putBytes(dst, []byte(s))
}

func sha(dst []byte) Digest { return sha256.Sum256(dst) }

// identityPayload binds the signed, stable identity of an event. The summary
// and the identity are covered together, so an attacker cannot keep an
// identity while swapping the attested content.
func identityPayload(id EventID, summary Digest) []byte {
	var b []byte
	b = putString(b, tagEventIdentity)
	b = putString(b, string(id))
	b = putBytes(b, summary[:])
	return b
}

// occurrencePayload binds one visible appearance of an event inside one
// partition. It never references the physical slot the host stored it in.
func occurrencePayload(p PartitionID, visSeq uint64, id EventID, summary Digest) []byte {
	var b []byte
	b = putString(b, tagOccurrence)
	b = putString(b, string(p))
	b = putUvar64(b, visSeq)
	b = putString(b, string(id))
	b = putBytes(b, summary[:])
	return b
}

// visibleChainHash folds the visible prefix in visibility order.
func visibleChainHash(events []*VisibleEvent) Digest {
	var acc Digest
	var b []byte
	b = putString(b, tagVisibleChain)
	b = putBytes(b, acc[:])
	for _, ev := range events {
		acc = sha(append(b, ev.OccurrencePayload...))
		b = putString(b, tagVisibleChain)
		b = putBytes(b, acc[:])
	}
	return acc
}

// logRootHash folds every committed entry (visible and hidden alike) in
// physical order. Hidden entries are sealed only by their opaque blobs, so the
// root proves the log length without exposing how many hidden events exist.
func logRootHash(entries []LogEntry) Digest {
	var acc Digest
	var b []byte
	b = putString(b, tagLogRoot)
	b = putBytes(b, acc[:])
	for _, entry := range entries {
		if entry.Visible != nil {
			b = append(b, 1)
			b = putBytes(b, entry.Visible.OccurrencePayload)
		} else {
			b = append(b, 0)
			b = putBytes(b, entry.HiddenBlob)
		}
		acc = sha(b)
		b = putString(b, tagLogRoot)
		b = putBytes(b, acc[:])
	}
	return acc
}

func rootPayload(r *PartitionRoot) []byte {
	var b []byte
	b = putString(b, tagRoot)
	b = putString(b, string(r.Partition))
	b = putUvar64(b, r.Epoch)
	b = putUvar64(b, r.LogLength)
	b = putBytes(b, r.LogRoot[:])
	b = putUvar64(b, r.VisibleLength)
	b = putBytes(b, r.VisibleChainHead[:])
	b = putUvar64(b, uint64(len(r.VisiblePrefixHeads)))
	for _, h := range r.VisiblePrefixHeads {
		b = putBytes(b, h[:])
	}
	return b
}

func receiptPayload(rc *Receipt) []byte {
	var b []byte
	b = putString(b, tagReceipt)
	b = putString(b, string(rc.Reader))
	b = putString(b, string(rc.Partition))
	b = putUvar64(b, rc.Epoch)
	b = putUvar64(b, rc.AuthorizedPrefix)
	b = putUvar64(b, rc.ConfirmedPrefix)
	return b
}

func revocationPayload(rev *Revocation) []byte {
	var b []byte
	b = putString(b, tagRevocation)
	b = putString(b, string(rev.Reader))
	b = putString(b, string(rev.Partition))
	b = putUvar64(b, rev.Epoch)
	b = putUvar64(b, rev.LastAuthorizedPrefix)
	return b
}

func cursorPayload(c *Cursor) []byte {
	var b []byte
	b = putString(b, tagCursor)
	b = putString(b, string(c.Reader))
	b = putString(b, string(c.ServiceKey))
	b = putUvar64(b, uint64(len(c.Progress)))
	for _, p := range c.Progress {
		b = putString(b, string(p.Partition))
		b = putUvar64(b, p.Epoch)
		b = putUvar64(b, p.AuthorizedPrefix)
		b = putUvar64(b, p.ConfirmedVisibleHead)
		b = putUvar64(b, p.MergedVisSeq)
	}
	b = putUvar64(b, uint64(len(c.Evidence)))
	for _, ev := range c.Evidence {
		b = putBytes(b, ev.OccurrencePayload)
		b = putBytes(b, ev.IdentitySig)
		b = putBytes(b, ev.OccurrenceSig)
	}
	b = putUvar64(b, uint64(len(c.Notifications)))
	for _, n := range c.Notifications {
		b = putString(b, string(n.EventID))
		b = putBytes(b, n.Summary[:])
		b = putUvar64(b, uint64(len(n.Sources)))
		for _, s := range n.Sources {
			b = putString(b, string(s.Partition))
			b = putUvar64(b, s.VisSeq)
		}
	}
	b = putUvar64(b, uint64(len(c.Receipts)))
	for _, rc := range c.Receipts {
		b = putBytes(b, receiptPayload(rc))
		b = putBytes(b, rc.Signature)
	}
	b = putBytes(b, c.RootsHash[:])
	return b
}

func verify(publicKey, message, sig []byte) bool {
	if len(publicKey) != ed25519.PublicKeySize || len(sig) != ed25519.SignatureSize {
		return false
	}
	return ed25519.Verify(ed25519.PublicKey(publicKey), message, sig)
}
