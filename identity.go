package netsec

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
)

// InstanceID is the random identity minted when an endpoint name is
// registered. The same name can be re-registered after deletion, but every
// registration gets a fresh InstanceID; existing connections keep the ID
// they were bound to at creation time.
type InstanceID [16]byte

// NewInstanceID draws a fresh random instance identifier.
func NewInstanceID() (InstanceID, error) {
	var id InstanceID
	if _, err := rand.Read(id[:]); err != nil {
		return InstanceID{}, fmt.Errorf("netsec: mint instance id: %w", err)
	}
	return id, nil
}

// String renders the identifier as lowercase hexadecimal.
func (id InstanceID) String() string {
	return hex.EncodeToString(id[:])
}

// Equal compares two instance identifiers in constant time.
func (id InstanceID) Equal(other InstanceID) bool {
	return subtle.ConstantTimeCompare(id[:], other[:]) == 1
}

// IsZero reports whether the identifier is the zero value.
func (id InstanceID) IsZero() bool {
	return id == InstanceID{}
}

// Challenge is the nonce the host issues while accepting a connection. The
// peer must prove possession of the challenge before any message is
// accepted.
type Challenge [32]byte

// NewChallenge draws a fresh random handshake challenge.
func NewChallenge() (Challenge, error) {
	var ch Challenge
	if _, err := rand.Read(ch[:]); err != nil {
		return Challenge{}, fmt.Errorf("netsec: mint challenge: %w", err)
	}
	return ch, nil
}

// Equal compares two challenges in constant time.
func (ch Challenge) Equal(other Challenge) bool {
	return subtle.ConstantTimeCompare(ch[:], other[:]) == 1
}

// PeerCreds captures the transport-level credentials of the remote end of a
// connection, as observed when the connection was created. On a real local
// transport these would be the OS process credentials obtained from the
// socket; the mock transport supplies synthetic credentials.
type PeerCreds struct {
	PID    int
	UID    int
	GID    int
	Labels map[string]string
}

// Equal reports whether two credential snapshots describe the same peer.
func (c PeerCreds) Equal(other PeerCreds) bool {
	if c.PID != other.PID || c.UID != other.UID || c.GID != other.GID {
		return false
	}
	if len(c.Labels) != len(other.Labels) {
		return false
	}
	for key, value := range c.Labels {
		if other.Labels[key] != value {
			return false
		}
	}
	return true
}
