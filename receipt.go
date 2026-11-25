package handoff

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"sync"
)

// EgressID identifies an exit/egress router.
type EgressID string

// Receipt is an egress-bound acknowledgement of one exact statement digest.
type Receipt struct {
	Egress      EgressID `json:"egress"`
	PlanID      string   `json:"plan_id"`
	Statement   Digest   `json:"statement"`
	Generation  uint64   `json:"generation"`
	ViewVersion uint64   `json:"view_version"`
	Nonce       uint64   `json:"nonce"`
}

type receiptDTO struct {
	Schema      string `json:"schema"`
	Egress      string `json:"egress"`
	PlanID      string `json:"plan_id"`
	Statement   string `json:"statement"`
	Generation  uint64 `json:"generation"`
	ViewVersion uint64 `json:"view_version"`
	Nonce       uint64 `json:"nonce"`
}

func (r Receipt) signingBytes() []byte {
	d := receiptDTO{
		Schema:      "handoff-receipt-v1",
		Egress:      string(r.Egress),
		PlanID:      r.PlanID,
		Statement:   r.Statement.String(),
		Generation:  r.Generation,
		ViewVersion: r.ViewVersion,
		Nonce:       r.Nonce,
	}
	b, _ := json.Marshal(d)
	return b
}

// SignedReceipt is an Ed25519-signed Receipt.
type SignedReceipt struct {
	Receipt   Receipt `json:"receipt"`
	Signature []byte  `json:"signature"`
}

// SignReceipt creates a deterministic-format Ed25519 receipt signature.
func SignReceipt(key ed25519.PrivateKey, r Receipt) SignedReceipt {
	sig := ed25519.Sign(key, r.signingBytes())
	return SignedReceipt{Receipt: r, Signature: sig}
}

// Verify checks the signature against key.
func (sr SignedReceipt) Verify(key ed25519.PublicKey) error {
	if len(key) != ed25519.PublicKeySize {
		return fmt.Errorf("%w: public key length %d", ErrBadSignature, len(key))
	}
	if len(sr.Signature) != ed25519.SignatureSize ||
		!ed25519.Verify(key, sr.Receipt.signingBytes(), sr.Signature) {
		return ErrBadSignature
	}
	return nil
}

// GenerateEgressKey returns a fresh Ed25519 key pair.
func GenerateEgressKey() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	return ed25519.GenerateKey(rand.Reader)
}

// ReceiptBook verifies trusted receipts and remembers replays and nonces.
type ReceiptBook struct {
	mu       sync.RWMutex
	keys     map[EgressID]ed25519.PublicKey
	byDigest map[EgressID]map[Digest]SignedReceipt
	nonces   map[EgressID]map[uint64]Digest
	Replays  int
	Invalid  int
}

// NewReceiptBook creates a book trusting exactly keys.
func NewReceiptBook(keys map[EgressID]ed25519.PublicKey) *ReceiptBook {
	copied := make(map[EgressID]ed25519.PublicKey, len(keys))
	for id, key := range keys {
		copied[id] = append(ed25519.PublicKey(nil), key...)
	}
	return &ReceiptBook{
		keys:     copied,
		byDigest: make(map[EgressID]map[Digest]SignedReceipt),
		nonces:   make(map[EgressID]map[uint64]Digest),
	}
}

// Observe verifies and inserts a receipt. It returns true only for the first
// valid use of that exact receipt/nonce. A replay returns false and ErrReplay.
func (b *ReceiptBook) Observe(sr SignedReceipt) (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	key, ok := b.keys[sr.Receipt.Egress]
	if !ok {
		b.Invalid++
		return false, ErrUnknownEgress
	}
	if err := sr.Verify(key); err != nil {
		b.Invalid++
		return false, err
	}

	digestMap := b.byDigest[sr.Receipt.Egress]
	if digestMap != nil {
		if existing, ok := digestMap[sr.Receipt.Statement]; ok {
			if equalSignature(existing.Signature, sr.Signature) &&
				existing.Receipt.Nonce == sr.Receipt.Nonce &&
				existing.Receipt.ViewVersion == sr.Receipt.ViewVersion {
				b.Replays++
				return false, fmt.Errorf("%w: exact receipt", ErrReplay)
			}
		}
	}

	nonceMap := b.nonces[sr.Receipt.Egress]
	if nonceMap != nil {
		if d, ok := nonceMap[sr.Receipt.Nonce]; ok {
			b.Replays++
			if d == sr.Receipt.Statement {
				return false, fmt.Errorf("%w: nonce", ErrReplay)
			}
			return false, fmt.Errorf("%w: nonce reused for %s", ErrReplay, d)
		}
	}

	if b.byDigest[sr.Receipt.Egress] == nil {
		b.byDigest[sr.Receipt.Egress] = make(map[Digest]SignedReceipt)
	}
	if b.nonces[sr.Receipt.Egress] == nil {
		b.nonces[sr.Receipt.Egress] = make(map[uint64]Digest)
	}
	b.byDigest[sr.Receipt.Egress][sr.Receipt.Statement] = sr
	b.nonces[sr.Receipt.Egress][sr.Receipt.Nonce] = sr.Receipt.Statement
	return true, nil
}

func equalSignature(a, c []byte) bool {
	if len(a) != len(c) {
		return false
	}
	for i := range a {
		if a[i] != c[i] {
			return false
		}
	}
	return true
}

// Has reports whether a valid receipt from egress exists for digest.
func (b *ReceiptBook) Has(egress EgressID, digest Digest) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	_, ok := b.byDigest[egress][digest]
	return ok
}

// Receipt returns the stored receipt, if any.
func (b *ReceiptBook) Receipt(egress EgressID, digest Digest) (SignedReceipt, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	sr, ok := b.byDigest[egress][digest]
	return sr, ok
}

// HasCurrentGeneration is like Has but also checks the signed generation.
func (b *ReceiptBook) HasCurrentGeneration(egress EgressID, s Statement) bool {
	sr, ok := b.Receipt(egress, s.Digest())
	return ok && sr.Receipt.Generation == s.Generation
}

// Missing returns egresses lacking an exact current-generation digest.
func (b *ReceiptBook) Missing(egresses []EgressID, s Statement) []EgressID {
	var out []EgressID
	for _, e := range egresses {
		if !b.HasCurrentGeneration(e, s) {
			out = append(out, e)
		}
	}
	return out
}
