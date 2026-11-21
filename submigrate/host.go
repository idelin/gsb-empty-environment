package submigrate

import (
	"crypto/ed25519"
	"crypto/sha256"
)

func deriveSeed(domain string, material []byte) []byte {
	h := sha256.New()
	h.Write([]byte(domain))
	h.Write(material)
	return h.Sum(nil)[:ed25519.SeedSize]
}

// Host is the reference trusted event host used by callers and tests. It owns
// one ed25519 identity and produces every signed artifact the service verifies.
type Host struct {
	key ed25519.PrivateKey
}

// NewHost builds a deterministic host key from the supplied seed material.
func NewHost(seedMaterial []byte) *Host {
	key := ed25519.NewKeyFromSeed(deriveSeed("submigrate/v1/host-key", seedMaterial))
	return &Host{key: key}
}

// PublicKey returns the host verification key.
func (h *Host) PublicKey() ed25519.PublicKey {
	return h.key.Public().(ed25519.PublicKey)
}

func (h *Host) sign(message []byte) []byte {
	return ed25519.Sign(h.key, message)
}

// Visible builds and signs one visible event without assigning a sequence.
func (h *Host) Visible(id EventID, summary Digest) *VisibleEvent {
	idPayload := identityPayload(id, summary)
	return &VisibleEvent{
		EventID:         id,
		Summary:         summary,
		IdentityPayload: idPayload,
		IdentitySig:     h.sign(idPayload),
	}
}

// Hidden builds an opaque hidden log slot. Its content is never revealed to
// subscribers and never carries a visible sequence number.
func (h *Host) Hidden(marker []byte) LogEntry {
	sum := sha256.Sum256(append([]byte("submigrate/v1/hidden/"), marker...))
	return LogEntry{HiddenBlob: sum[:]}
}

// BuildPartition commits a physical entry list and returns the signed root and
// the ordered visible events. Occurrence signatures are issued here, covering
// only visibility coordinates (never physical positions).
func (h *Host) BuildPartition(p PartitionID, epoch uint64, entries []LogEntry) (*PartitionRoot, []*VisibleEvent) {
	visible := make([]*VisibleEvent, 0, len(entries))
	prefixHeads := []Digest{{}}
	visSeq := uint64(0)
	for i := range entries {
		ev := entries[i].Visible
		if ev == nil {
			continue
		}
		visSeq++
		ev.Partition = p
		ev.VisSeq = visSeq
		ev.OccurrencePayload = occurrencePayload(p, visSeq, ev.EventID, ev.Summary)
		ev.OccurrenceSig = h.sign(ev.OccurrencePayload)
		visible = append(visible, ev)
		var base []byte
		base = putString(base, tagVisibleChain)
		base = putBytes(base, prefixHeads[len(prefixHeads)-1][:])
		prefixHeads = append(prefixHeads, sha(append(base, ev.OccurrencePayload...)))
	}

	root := &PartitionRoot{
		Partition:          p,
		Epoch:              epoch,
		LogLength:          uint64(len(entries)),
		LogRoot:            logRootHash(entries),
		VisibleLength:      uint64(len(visible)),
		VisibleChainHead:   visibleChainHash(visible),
		VisiblePrefixHeads: prefixHeads,
		HostKey:            []byte(h.PublicKey()),
	}
	root.Signature = h.sign(rootPayload(root))
	return root, visible
}

// IssueReceipt signs an old reader's authorization and confirmation prefixes.
func (h *Host) IssueReceipt(reader ReaderID, p PartitionID, epoch, authorized, confirmed uint64) *Receipt {
	rc := &Receipt{
		Reader:           reader,
		Partition:        p,
		Epoch:            epoch,
		AuthorizedPrefix: authorized,
		ConfirmedPrefix:  confirmed,
	}
	rc.Signature = h.sign(receiptPayload(rc))
	return rc
}

// Revoke signs an explicit withdrawal for a reader/partition grant.
func (h *Host) Revoke(reader ReaderID, p PartitionID, epoch, lastAuthorized uint64) *Revocation {
	rev := &Revocation{
		Reader:               reader,
		Partition:            p,
		Epoch:                epoch,
		LastAuthorizedPrefix: lastAuthorized,
	}
	rev.Signature = h.sign(revocationPayload(rev))
	return rev
}

// Merge concatenates host-supplied partial orders into one merged stream. Each
// input slice must already be in within-partition visibility order.
func (h *Host) Merge(partials ...[]*VisibleEvent) []*VisibleEvent {
	total := 0
	for _, part := range partials {
		total += len(part)
	}
	merged := make([]*VisibleEvent, 0, total)
	for _, part := range partials {
		merged = append(merged, part...)
	}
	return merged
}
