package netsec

import (
	"crypto/ed25519"
	"errors"
	"os"
	"sync"
	"time"
)

// Station is the importable offline authorization component running inside a
// charging station service. It is safe for concurrent use.
//
// The component holds no cleartext authorization material: only the digest of
// each backend-signed grant and its remaining-quota counter are persisted.
type Station struct {
	mu         sync.RWMutex
	stationID  string
	privateKey ed25519.PrivateKey
	backendKey ed25519.PublicKey
	dataDir    string
	now        func() time.Time

	// entries maps an authorization digest to its remaining quota.
	entries map[string]int64
	// nextSeq is the sequence number assigned to the next receipt.
	nextSeq uint64
	// chainHead is the current per-station hash-chain head.
	chainHead []byte
	// outbox holds signed receipts not yet acknowledged by the backend.
	outbox []SignedReceipt
}

// OpenStation opens (or initializes) the local component for one physical
// charging station. DataDir holds the digest-only store and the receipt
// outbox; the directory and its files are created owner-only.
func OpenStation(cfg StationConfig) (*Station, error) {
	if cfg.StationID == "" || cfg.DataDir == "" ||
		len(cfg.PrivateKey) != ed25519.PrivateKeySize ||
		len(cfg.BackendKey) != ed25519.PublicKeySize {
		return nil, ErrInvalidConfig
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	s := &Station{
		stationID:  cfg.StationID,
		privateKey: append(ed25519.PrivateKey(nil), cfg.PrivateKey...),
		backendKey: append(ed25519.PublicKey(nil), cfg.BackendKey...),
		dataDir:    cfg.DataDir,
		now:        now,
		entries:    make(map[string]int64),
	}
	if err := os.MkdirAll(cfg.DataDir, storageDirMode); err != nil {
		return nil, err
	}

	var state persistedState
	switch err := loadJSON(statePath(cfg.DataDir), &state); {
	case err == nil:
		// Copy guard: a store created for another station must never be used
		// here, even if the attacker copied the entire directory.
		if state.StationID != cfg.StationID {
			return nil, ErrCopiedStore
		}
		for _, entry := range state.Entries {
			s.entries[entry.Digest] = entry.Remaining
		}
		var out persistedOutbox
		if err := loadJSON(outboxPath(cfg.DataDir), &out); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		if out.StationID != "" && out.StationID != cfg.StationID {
			return nil, ErrCopiedStore
		}
		s.outbox = append([]SignedReceipt(nil), out.Receipts...)
		// Reconcile counters, sequence and chain head with the durable outbox.
		// The state file may lag by one crash window (outbox is written first),
		// so receipts the state file has not accounted for are replayed.
		s.nextSeq = state.NextSeq
		s.chainHead = append([]byte(nil), state.ChainHead...)
		if len(s.outbox) > 0 {
			head := append([]byte(nil), s.chainHead...)
			var lastSeq uint64
			for _, signed := range s.outbox {
				if signed.Receipt.Seq >= s.nextSeq {
					if !equalBytes(head, signed.Receipt.PrevHash) {
						return nil, ErrChainBroken
					}
					remaining, ok := s.entries[signed.Receipt.Digest]
					if !ok || signed.Receipt.Amount > remaining {
						return nil, ErrQuotaExceeded
					}
					s.entries[signed.Receipt.Digest] = remaining - signed.Receipt.Amount
					head = ReceiptHead(signed.Receipt)
					lastSeq = signed.Receipt.Seq
				} else {
					head = ReceiptHead(signed.Receipt)
				}
			}
			if lastSeq >= s.nextSeq {
				s.chainHead = head
				s.nextSeq = lastSeq + 1
			}
		}
	case errors.Is(err, os.ErrNotExist):
		s.nextSeq = 1
		s.chainHead = GenesisHead(cfg.StationID)
		if err := s.persistLocked(); err != nil {
			return nil, err
		}
	default:
		return nil, err
	}
	return s, nil
}

// verifyToken decodes a wire token, checks the backend signature and the
// station binding, and validates the business fields.
func (s *Station) verifyToken(wire []byte) (SignedToken, error) {
	token, err := DecodeToken(wire)
	if err != nil {
		return SignedToken{}, err
	}
	if !ed25519.Verify(s.backendKey, canonicalAuth(token.Auth), token.Signature) {
		return SignedToken{}, ErrBadSignature
	}
	if err := validateAuthorization(token.Auth); err != nil {
		return SignedToken{}, err
	}
	if token.Auth.StationID != s.stationID {
		return SignedToken{}, ErrForeignStation
	}
	return token, nil
}

func validateAuthorization(a Authorization) error {
	if a.AuthID == "" || a.StationID == "" || a.ConnectorID == "" || a.AccountID == "" {
		return ErrInvalidAuthorization
	}
	if a.Quota <= 0 || a.ValidFrom <= 0 || a.ValidUntil <= 0 || a.ValidFrom > a.ValidUntil {
		return ErrInvalidAuthorization
	}
	return nil
}

// InstallList performs a full-list synchronization: it validates every token
// first and applies the new list only when all tokens are genuine,
// well-formed and bound to this station. Authorizations absent from the new
// list are revoked and their digest/counter records removed, while grants
// already present keep their counters so re-synchronizing cannot reset quota.
func (s *Station) InstallList(wireTokens [][]byte) error {
	type pending struct {
		digest string
		quota  int64
	}
	seen := make(map[string]struct{})
	pendingEntries := make([]pending, 0, len(wireTokens))
	for _, wire := range wireTokens {
		token, err := s.verifyToken(wire)
		if err != nil {
			return err
		}
		digest := AuthDigest(token.Auth)
		if _, dup := seen[digest]; dup {
			return ErrInvalidToken
		}
		seen[digest] = struct{}{}
		pendingEntries = append(pendingEntries, pending{digest: digest, quota: token.Auth.Quota})
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	nextEntries := make(map[string]int64, len(pendingEntries))
	for _, entry := range pendingEntries {
		if remaining, exists := s.entries[entry.digest]; exists {
			nextEntries[entry.digest] = remaining
		} else {
			nextEntries[entry.digest] = entry.quota
		}
	}
	s.entries = nextEntries
	return s.persistLocked()
}

// Consume atomically deducts amount from the authorization in wire, subject to
// signature, station/connector binding, validity window and remaining quota.
// On success it returns a station-signed, hash-chained receipt that is also
// enqueued for the next Flush.
func (s *Station) Consume(wire []byte, connectorID string, amount int64) (SignedReceipt, error) {
	token, err := s.verifyToken(wire)
	if err != nil {
		return SignedReceipt{}, err
	}
	if amount <= 0 {
		return SignedReceipt{}, ErrInvalidAmount
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now().Unix()
	if now < token.Auth.ValidFrom {
		return SignedReceipt{}, ErrNotYetValid
	}
	if now > token.Auth.ValidUntil {
		return SignedReceipt{}, ErrExpired
	}
	if connectorID != token.Auth.ConnectorID {
		return SignedReceipt{}, ErrForeignStation
	}
	digest := AuthDigest(token.Auth)
	remaining, ok := s.entries[digest]
	if !ok {
		return SignedReceipt{}, ErrUnknownAuthorization
	}
	if amount > remaining {
		return SignedReceipt{}, ErrQuotaExceeded
	}

	receipt := Receipt{
		AuthID:      token.Auth.AuthID,
		Digest:      digest,
		StationID:   s.stationID,
		ConnectorID: token.Auth.ConnectorID,
		AccountID:   token.Auth.AccountID,
		Amount:      amount,
		ConsumedAt:  now,
		Seq:         s.nextSeq,
		PrevHash:    append([]byte(nil), s.chainHead...),
	}
	signed := SignedReceipt{
		Receipt:   receipt,
		Signature: ed25519.Sign(s.privateKey, canonicalReceipt(receipt)),
	}

	s.entries[digest] = remaining - amount
	s.chainHead = ReceiptHead(receipt)
	s.nextSeq++
	s.outbox = append(s.outbox, signed)
	if err := s.persistLocked(); err != nil {
		// Persistence failures are fatal for the local state; surface them.
		return SignedReceipt{}, err
	}
	return signed, nil
}

// Flush returns all receipts not yet acknowledged by the backend, in sequence
// order. The returned slice is a copy safe to mutate.
func (s *Station) Flush() []SignedReceipt {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]SignedReceipt(nil), s.outbox...)
}

// PendingReceiptCount reports how many receipts await backend acknowledgement.
func (s *Station) PendingReceiptCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.outbox)
}

// Ack drops the first count receipts after the backend has accepted them.
func (s *Station) Ack(count int) error {
	if count < 0 {
		return ErrInvalidReceipt
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if count > len(s.outbox) {
		return ErrInvalidReceipt
	}
	s.outbox = append([]SignedReceipt(nil), s.outbox[count:]...)
	return s.persistLocked()
}

// Remaining verifies a token and returns its locally tracked remaining quota.
func (s *Station) Remaining(wire []byte) (int64, error) {
	token, err := s.verifyToken(wire)
	if err != nil {
		return 0, err
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	remaining, ok := s.entries[AuthDigest(token.Auth)]
	if !ok {
		return 0, ErrUnknownAuthorization
	}
	return remaining, nil
}
