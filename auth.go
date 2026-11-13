package netsec

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
)

const (
	// IdentityChallengeDomain separates this challenge from other signatures.
	IdentityChallengeDomain = "example.org/netsec/q0127/identity-challenge/v1"

	// MinNonceSize is the minimum accepted challenge nonce size. Callers should
	// use GenerateNonce to obtain a cryptographically random nonce.
	MinNonceSize = 16
)

// Challenge is the host-side link-layer identity challenge. The nonce must come
// from a CSPRNG and must not repeat for a connection. ConnectionID is the link
// layer connection identifier.
type Challenge struct {
	Nonce        []byte
	ConnectionID uint64
}

// IdentityProof is the device's signature by its long-term identity key.
type IdentityProof struct {
	Signature []byte
}

// Privilege is the access level granted by a completed connection.
type Privilege uint8

const (
	// PrivilegeNone means no identity was published.
	PrivilegeNone Privilege = iota

	// PrivilegeDiscovery is the restricted level produced by a retained old
	// IRK. It cannot be upgraded by address resolution alone.
	PrivilegeDiscovery

	// PrivilegeFull is granted by a current-IRK ticket after the long-term key
	// challenge succeeds.
	PrivilegeFull
)

func (p Privilege) String() string {
	switch p {
	case PrivilegeNone:
		return "none"
	case PrivilegeDiscovery:
		return "discovery"
	case PrivilegeFull:
		return "full"
	default:
		return fmt.Sprintf("unknown(%d)", p)
	}
}

// Identity is published only after Complete revalidates the ticket and verifies
// the device's long-term-key signature.
type Identity struct {
	DeviceID                 DeviceID
	Address                  Address
	ConnectionID             uint64
	RecordGeneration         RecordGeneration
	TableVersion             TableVersion
	Privilege                Privilege
	ReauthenticationRequired bool
}

// HasFullPrivilege reports whether this identity may exercise full paired-device
// permissions. A legacy-IRK discovery session never does.
func (i Identity) HasFullPrivilege() bool {
	return i.Privilege == PrivilegeFull && !i.ReauthenticationRequired
}

// GenerateNonce returns a fresh 32-byte challenge nonce.
func GenerateNonce() ([]byte, error) {
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("netsec: generate identity nonce: %w", err)
	}
	return nonce, nil
}

// ChallengeMessage returns the exact byte string the device's long-term key
// must sign. It covers a domain separator, the random nonce, the six address
// octets in network byte order, and the connection ID.
func ChallengeMessage(address Address, connectionID uint64, nonce []byte) ([]byte, error) {
	if err := address.Validate(); err != nil {
		return nil, err
	}
	if len(nonce) < MinNonceSize {
		return nil, fmt.Errorf("%w: nonce length %d, want at least %d", ErrInvalidChallenge, len(nonce), MinNonceSize)
	}

	message := make([]byte, 0, len(IdentityChallengeDomain)+1+4+len(nonce)+AddressSize+8)
	message = append(message, IdentityChallengeDomain...)
	message = append(message, 0)

	var encoded [8]byte
	binary.BigEndian.PutUint32(encoded[:4], uint32(len(nonce)))
	message = append(message, encoded[:4]...)
	message = append(message, nonce...)
	message = append(message, address[:]...)
	binary.BigEndian.PutUint64(encoded[:], connectionID)
	message = append(message, encoded[:]...)
	return message, nil
}

// Complete revalidates a ticket against the current immutable table snapshot
// and verifies the device's long-term-key signature over nonce||address||
// connection ID. Identity is published only after all checks pass.
//
// If a publisher was configured it is invoked exactly once on success. A
// legacy-IRK ticket completes only with PrivilegeDiscovery and
// ReauthenticationRequired set; it never restores full permissions.
//
// Complete holds a read lock over the table from revalidation through
// publication, so a concurrent table mutation cannot race the version check.
// Callbacks must not call back into the resolver to mutate its table.
func (r *Resolver) Complete(ticket Ticket, challenge Challenge, proof IdentityProof) (*Identity, error) {
	if ticket.IsZero() || !r.ticketIsAuthentic(ticket) {
		return nil, ErrTicketInvalid
	}
	if len(challenge.Nonce) < MinNonceSize {
		return nil, fmt.Errorf("%w: nonce length %d, want at least %d", ErrInvalidChallenge, len(challenge.Nonce), MinNonceSize)
	}
	if len(proof.Signature) == 0 {
		return nil, fmt.Errorf("%w: empty signature", ErrIdentityProof)
	}
	if r.verifySignature == nil {
		return nil, ErrSignatureVerifier
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	snapshot := r.snapshot.Load()
	entry, ok := snapshot.byID[ticket.deviceID]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrDeviceNotFound, ticket.deviceID)
	}
	if entry.record.State != RecordActive {
		return nil, fmt.Errorf("%w: %s", ErrDeviceRevoked, ticket.deviceID)
	}
	if entry.record.Generation != ticket.generation {
		return nil, fmt.Errorf(
			"%w: device %s ticket=%d current=%d",
			ErrStaleGeneration,
			ticket.deviceID,
			ticket.generation,
			entry.record.Generation,
		)
	}
	if snapshot.version != ticket.tableVersion {
		return nil, fmt.Errorf(
			"%w: ticket=%d current=%d",
			ErrStaleTable,
			ticket.tableVersion,
			snapshot.version,
		)
	}

	matchedIRK, err := r.revalidateCredential(entry, ticket)
	if err != nil {
		return nil, err
	}
	hash, err := r.addressHash(matchedIRK, ticket.address)
	if err != nil {
		return nil, fmt.Errorf("netsec: AES callback while completing ticket: %w", err)
	}
	if hash != ticket.address.Hash() {
		return nil, fmt.Errorf("%w: address hash changed", ErrStaleCredential)
	}

	if len(entry.record.IdentityPublicKey) == 0 {
		return nil, fmt.Errorf("%w: %s", ErrIdentityPublicKey, ticket.deviceID)
	}
	message, err := ChallengeMessage(ticket.address, challenge.ConnectionID, challenge.Nonce)
	if err != nil {
		return nil, err
	}
	publicKey := append([]byte(nil), entry.record.IdentityPublicKey...)
	if err := r.verifySignature(publicKey, message, proof.Signature); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrIdentityProof, err)
	}

	identity := &Identity{
		DeviceID:                 ticket.deviceID,
		Address:                  ticket.address,
		ConnectionID:             challenge.ConnectionID,
		RecordGeneration:         entry.record.Generation,
		TableVersion:             snapshot.version,
		Privilege:                PrivilegeFull,
		ReauthenticationRequired: false,
	}
	if ticket.kind == credentialRetired {
		identity.Privilege = PrivilegeDiscovery
		identity.ReauthenticationRequired = true
	}

	if r.publishIdentity != nil {
		if err := r.publishIdentity(*identity); err != nil {
			return nil, fmt.Errorf("netsec: publish identity: %w", err)
		}
	}
	return identity, nil
}

func (r *Resolver) revalidateCredential(entry *deviceEntry, ticket Ticket) (IRK, error) {
	switch ticket.kind {
	case credentialCurrent:
		if currentCredentialFingerprint(entry.record.IRK) != ticket.credential {
			return IRK{}, fmt.Errorf("%w: current credential changed", ErrStaleCredential)
		}
		return entry.record.IRK, nil

	case credentialRetired:
		for index := range entry.retired {
			old := &entry.retired[index]
			if old.fingerprint != ticket.credential {
				continue
			}
			if old.expiresAt.UnixNano() != ticket.legacyUntil.UnixNano() {
				return IRK{}, fmt.Errorf("%w: legacy credential expiry changed", ErrStaleCredential)
			}
			if !r.now().Before(old.expiresAt) {
				return IRK{}, fmt.Errorf("%w: device %s", ErrLegacyIRKExpired, ticket.deviceID)
			}
			return old.irk, nil
		}
		return IRK{}, fmt.Errorf("%w: device %s", ErrLegacyIRKExpired, ticket.deviceID)

	default:
		return IRK{}, fmt.Errorf("%w: unknown credential kind", ErrTicketInvalid)
	}
}
