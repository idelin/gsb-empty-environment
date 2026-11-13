package netsec

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"time"
)

const ticketSealDomain = "example.org/netsec/q0127/ticket/v1"

type credentialKind uint8

const (
	credentialCurrent credentialKind = iota + 1
	credentialRetired
)

// Ticket is an opaque, tamper-evident resolution result. It binds the device
// identity, record generation, private address, and table version at the
// instant Resolve loaded its immutable table snapshot.
//
// Tickets are issued only by Resolve. Their fields are intentionally private
// and sealed with a resolver-local HMAC key so callers cannot edit a ticket or
// construct one from public fields.
type Ticket struct {
	deviceID       DeviceID
	generation     RecordGeneration
	address        Address
	tableVersion   TableVersion
	requiresReauth bool
	kind           credentialKind
	credential     [32]byte
	legacyUntil    time.Time
	seal           [32]byte
}

// DeviceID returns the resolved device identity.
func (t Ticket) DeviceID() DeviceID {
	return t.deviceID
}

// RecordGeneration returns the device record generation fixed at resolution.
func (t Ticket) RecordGeneration() RecordGeneration {
	return t.generation
}

// Address returns the private address fixed at resolution.
func (t Ticket) Address() Address {
	return t.address
}

// TableVersion returns the immutable table version fixed at resolution.
func (t Ticket) TableVersion() TableVersion {
	return t.tableVersion
}

// RequiresReauthentication reports whether the ticket was produced by a
// retained old IRK. Such a ticket is suitable for discovery only.
func (t Ticket) RequiresReauthentication() bool {
	return t.requiresReauth
}

// IsZero reports whether the ticket was not issued.
func (t Ticket) IsZero() bool {
	return t.seal == [32]byte{}
}

func (t Ticket) String() string {
	if t.IsZero() {
		return "ticket(anonymous)"
	}
	return fmt.Sprintf(
		"ticket(device=%s generation=%d address=%s table=%d reauth=%t)",
		t.deviceID,
		t.generation,
		t.address,
		t.tableVersion,
		t.requiresReauth,
	)
}

func (r *Resolver) issueTicket(
	entry *deviceEntry,
	address Address,
	tableVersion TableVersion,
	kind credentialKind,
	credential [32]byte,
	legacyUntil time.Time,
) Ticket {
	ticket := Ticket{
		deviceID:       entry.record.DeviceID,
		generation:     entry.record.Generation,
		address:        address,
		tableVersion:   tableVersion,
		requiresReauth: kind == credentialRetired,
		kind:           kind,
		credential:     credential,
		legacyUntil:    legacyUntil,
	}
	ticket.seal = r.sealTicket(ticket)
	return ticket
}

func (r *Resolver) sealTicket(ticket Ticket) [32]byte {
	mac := hmac.New(sha256.New, r.ticketKey[:])
	_, _ = mac.Write([]byte(ticketSealDomain))

	writeUint64(mac, uint64(ticket.tableVersion))
	writeUint64(mac, uint64(ticket.generation))
	writeUint64(mac, uint64(len(ticket.deviceID)))
	_, _ = mac.Write([]byte(ticket.deviceID))
	_, _ = mac.Write(ticket.address[:])
	_, _ = mac.Write([]byte{byte(ticket.kind)})
	if ticket.requiresReauth {
		_, _ = mac.Write([]byte{1})
	} else {
		_, _ = mac.Write([]byte{0})
	}
	_, _ = mac.Write(ticket.credential[:])
	if ticket.legacyUntil.IsZero() {
		writeUint64(mac, 0)
	} else {
		writeUint64(mac, uint64(ticket.legacyUntil.UnixNano()))
	}

	var seal [32]byte
	copy(seal[:], mac.Sum(nil))
	return seal
}

func (r *Resolver) ticketIsAuthentic(ticket Ticket) bool {
	expected := r.sealTicket(ticket)
	return hmac.Equal(expected[:], ticket.seal[:])
}

func writeUint64(mac interface{ Write([]byte) (int, error) }, value uint64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	_, _ = mac.Write(encoded[:])
}
