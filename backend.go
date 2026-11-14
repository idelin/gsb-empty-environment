package netsec

import (
	"crypto/ed25519"
	"sync"
)

// issuedAuth is the backend's bookkeeping record for one issued grant.
type issuedAuth struct {
	auth     Authorization
	digest   string
	consumed int64
}

type stationRecord struct {
	publicKey ed25519.PublicKey
	nextSeq   uint64
	chainHead []byte
}

// Backend is the online authorization service that signs grants and verifies
// receipts reported by charging stations. It is safe for concurrent use.
type Backend struct {
	mu          sync.RWMutex
	privateKey  ed25519.PrivateKey
	authorities map[string]*issuedAuth
	stations    map[string]*stationRecord
}

// NewBackend creates a backend holding the long-term Ed25519 signing key whose
// public part is provisioned to every station.
func NewBackend(privateKey ed25519.PrivateKey) (*Backend, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return nil, ErrInvalidKey
	}
	return &Backend{
		privateKey:  append(ed25519.PrivateKey(nil), privateKey...),
		authorities: make(map[string]*issuedAuth),
		stations:    make(map[string]*stationRecord),
	}, nil
}

// PublicKey returns the public key stations use to verify tokens.
func (b *Backend) PublicKey() ed25519.PublicKey {
	pub := b.privateKey.Public().(ed25519.PublicKey)
	return append(ed25519.PublicKey(nil), pub...)
}

// RegisterStation enrolls a charging station and binds its receipt-signing
// public key to the station identifier. Re-registration with another key is
// rejected so station identities cannot be silently hijacked.
func (b *Backend) RegisterStation(stationID string, publicKey ed25519.PublicKey) error {
	if stationID == "" || len(publicKey) != ed25519.PublicKeySize {
		return ErrInvalidConfig
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if existing, ok := b.stations[stationID]; ok {
		if !existing.publicKey.Equal(publicKey) {
			return ErrStationExists
		}
		return nil
	}
	b.stations[stationID] = &stationRecord{
		publicKey: append(ed25519.PublicKey(nil), publicKey...),
		nextSeq:   1,
		chainHead: GenesisHead(stationID),
	}
	return nil
}

// Issue validates and signs one authorization, returning the wire token that
// can be pushed to the target station during synchronization.
func (b *Backend) Issue(a Authorization) ([]byte, error) {
	if err := validateAuthorization(a); err != nil {
		return nil, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, exists := b.authorities[a.AuthID]; exists {
		return nil, ErrAuthorizationExists
	}
	if _, known := b.stations[a.StationID]; !known {
		return nil, ErrUnknownStation
	}
	token := SignedToken{
		Auth:      a,
		Signature: ed25519.Sign(b.privateKey, canonicalAuth(a)),
	}
	b.authorities[a.AuthID] = &issuedAuth{
		auth:     a,
		digest:   AuthDigest(a),
		consumed: 0,
	}
	return EncodeToken(token)
}

// ReceiveReceipt verifies and applies one receipt reported by a station. It
// enforces station signature, station binding, strict sequence order, hash
// chain integrity, grant matching, validity window and aggregate quota, so
// replayed, copied or over-quota consumption is rejected.
func (b *Backend) ReceiveReceipt(wire []byte) error {
	signed, err := DecodeReceipt(wire)
	if err != nil {
		return err
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	station, ok := b.stations[signed.Receipt.StationID]
	if !ok {
		return ErrUnknownStation
	}
	if !ed25519.Verify(station.publicKey, canonicalReceipt(signed.Receipt), signed.Signature) {
		return ErrBadSignature
	}
	if signed.Receipt.Seq != station.nextSeq {
		if signed.Receipt.Seq < station.nextSeq {
			return ErrReplay
		}
		return ErrSequenceGap
	}
	expectedPrev := station.chainHead
	if !equalBytes(expectedPrev, signed.Receipt.PrevHash) {
		return ErrChainBroken
	}

	record, ok := b.authorities[signed.Receipt.AuthID]
	if !ok {
		return ErrUnknownAuthorization
	}
	r := signed.Receipt
	if r.Digest != record.digest ||
		r.StationID != record.auth.StationID ||
		r.ConnectorID != record.auth.ConnectorID ||
		r.AccountID != record.auth.AccountID {
		return ErrReceiptMismatch
	}
	if r.Amount <= 0 {
		return ErrInvalidAmount
	}
	if r.ConsumedAt < record.auth.ValidFrom || r.ConsumedAt > record.auth.ValidUntil {
		return ErrExpired
	}
	if record.consumed+r.Amount > record.auth.Quota {
		return ErrQuotaExceeded
	}

	record.consumed += r.Amount
	station.nextSeq++
	station.chainHead = ReceiptHead(r)
	return nil
}

// ConsumedTotal returns the aggregate quota already accepted for an
// authorization. Intended for tests and operator reconciliation.
func (b *Backend) ConsumedTotal(authID string) (int64, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	record, ok := b.authorities[authID]
	if !ok {
		return 0, ErrUnknownAuthorization
	}
	return record.consumed, nil
}

// IssuedDigest returns the digest under which the backend issued authID.
func (b *Backend) IssuedDigest(authID string) (string, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	record, ok := b.authorities[authID]
	if !ok {
		return "", ErrUnknownAuthorization
	}
	return record.digest, nil
}

// StationChainHead returns the backend's current hash-chain head for a
// station, enabling reconciliation tests.
func (b *Backend) StationChainHead(stationID string) ([]byte, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	station, ok := b.stations[stationID]
	if !ok {
		return nil, ErrUnknownStation
	}
	return append([]byte(nil), station.chainHead...), nil
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
