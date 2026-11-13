package netsec

import (
	"crypto/rand"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// AESFunc is the host AES primitive used for the problem's address hash:
// AES(IRK, prand||zero padding), taking the first three output octets. The
// callback must be safe for concurrent use. It is provided explicitly so the
// host controls key storage and so tests can substitute a collision-producing
// implementation.
type AESFunc func(key IRK, block [16]byte) ([16]byte, error)

// SignatureVerifier verifies a device long-term-key signature over message
// using publicKey. A nil error means the signature is valid. The callback must
// be safe for concurrent use.
type SignatureVerifier func(publicKey, message, signature []byte) error

// IdentityPublisher is called only after every ticket and identity check has
// passed. It receives a value copy of the identity. A non-nil error prevents
// Complete from returning an identity.
type IdentityPublisher func(identity Identity) error

// Options configures a Resolver.
type Options struct {
	AES             AESFunc
	VerifySignature SignatureVerifier
	PublishIdentity IdentityPublisher
	OldIRKRetention time.Duration
	Now             func() time.Time
}

// Resolver owns an atomically replaced IRK table and issues resolution tickets.
// All resolution within one call uses exactly one immutable table snapshot.
type Resolver struct {
	aes             AESFunc
	verifySignature SignatureVerifier
	publishIdentity IdentityPublisher
	oldIRKRetention time.Duration
	now             func() time.Time
	ticketKey       [32]byte
	snapshot        atomic.Pointer[tableSnapshot]
	mu              sync.RWMutex
}

// NewResolver constructs an empty resolver.
func NewResolver(options Options) (*Resolver, error) {
	if options.AES == nil {
		return nil, ErrAESUnavailable
	}
	if options.OldIRKRetention < 0 {
		return nil, fmt.Errorf("%w: negative old-IRK retention", ErrInvalidConfiguration)
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}

	resolver := &Resolver{
		aes:             options.AES,
		verifySignature: options.VerifySignature,
		publishIdentity: options.PublishIdentity,
		oldIRKRetention: options.OldIRKRetention,
		now:             now,
	}
	if _, err := rand.Read(resolver.ticketKey[:]); err != nil {
		return nil, fmt.Errorf("netsec: generate ticket key: %w", err)
	}
	resolver.snapshot.Store(newTableSnapshot())
	return resolver, nil
}

// ResolutionStatus describes the outcome of address resolution.
type ResolutionStatus uint8

const (
	// ResolutionAnonymous means no Active record matched.
	ResolutionAnonymous ResolutionStatus = iota

	// ResolutionUnique means exactly one Active record matched and a ticket
	// was issued.
	ResolutionUnique

	// ResolutionConflict means more than one Active record matched. No ticket
	// is issued and Conflicts lists every matching device ID.
	ResolutionConflict
)

func (s ResolutionStatus) String() string {
	switch s {
	case ResolutionAnonymous:
		return "anonymous"
	case ResolutionUnique:
		return "unique"
	case ResolutionConflict:
		return "conflict"
	default:
		return fmt.Sprintf("unknown(%d)", s)
	}
}

// Resolution is the result of resolving one private address.
type Resolution struct {
	Status    ResolutionStatus
	Address   Address
	Ticket    Ticket
	Conflicts []DeviceID
}

// IsAnonymous reports whether no record matched.
func (r Resolution) IsAnonymous() bool {
	return r.Status == ResolutionAnonymous
}

// IsUnique reports whether a ticket was issued.
func (r Resolution) IsUnique() bool {
	return r.Status == ResolutionUnique
}

// Resolve matches address against every Active record in one fixed table
// snapshot. Exactly one matching device is required to issue a ticket. Zero
// matches are anonymous; multiple matches are rejected with ConflictError and
// never select the first result.
func (r *Resolver) Resolve(address Address) (Resolution, error) {
	if err := address.Validate(); err != nil {
		return Resolution{Status: ResolutionAnonymous, Address: address}, err
	}
	return r.resolveSnapshot(r.snapshot.Load(), address, r.now())
}

func (r *Resolver) resolveSnapshot(snapshot *tableSnapshot, address Address, now time.Time) (Resolution, error) {
	target := address.Hash()
	type match struct {
		entry       *deviceEntry
		kind        credentialKind
		credential  [32]byte
		legacyUntil time.Time
	}

	var matches []match
	for _, entry := range snapshot.records {
		if entry.record.State != RecordActive {
			continue
		}

		hash, err := r.addressHash(entry.record.IRK, address)
		if err != nil {
			return Resolution{Address: address}, fmt.Errorf(
				"netsec: AES callback for device %s: %w", entry.record.DeviceID, err,
			)
		}
		if hash == target {
			matches = append(matches, match{
				entry:      entry,
				kind:       credentialCurrent,
				credential: currentCredentialFingerprint(entry.record.IRK),
			})
			continue
		}

		for index := range entry.retired {
			old := &entry.retired[index]
			if !now.Before(old.expiresAt) {
				continue
			}
			hash, err := r.addressHash(old.irk, address)
			if err != nil {
				return Resolution{Address: address}, fmt.Errorf(
					"netsec: AES callback for retired IRK of device %s: %w", entry.record.DeviceID, err,
				)
			}
			if hash == target {
				matches = append(matches, match{
					entry:       entry,
					kind:        credentialRetired,
					credential:  old.fingerprint,
					legacyUntil: old.expiresAt,
				})
				break
			}
		}
	}

	switch len(matches) {
	case 0:
		return Resolution{Status: ResolutionAnonymous, Address: address}, nil
	case 1:
		match := matches[0]
		ticket := r.issueTicket(
			match.entry,
			address,
			snapshot.version,
			match.kind,
			match.credential,
			match.legacyUntil,
		)
		return Resolution{
			Status:  ResolutionUnique,
			Address: address,
			Ticket:  ticket,
		}, nil
	default:
		deviceIDs := make([]DeviceID, 0, len(matches))
		for _, match := range matches {
			deviceIDs = append(deviceIDs, match.entry.record.DeviceID)
		}
		sort.Slice(deviceIDs, func(i, j int) bool {
			return deviceIDs[i] < deviceIDs[j]
		})
		conflict := &ConflictError{
			Address:   address,
			DeviceIDs: append([]DeviceID(nil), deviceIDs...),
		}
		return Resolution{
			Status:    ResolutionConflict,
			Address:   address,
			Conflicts: deviceIDs,
		}, conflict
	}
}

func (r *Resolver) addressHash(irk IRK, address Address) ([3]byte, error) {
	var block [16]byte
	copy(block[:3], address[3:])
	output, err := r.aes(irk, block)
	if err != nil {
		return [3]byte{}, err
	}
	return [3]byte{output[0], output[1], output[2]}, nil
}
