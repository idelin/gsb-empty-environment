package netsec

import (
	"crypto/sha256"
	"fmt"
	"strings"
)

// HopProof is one trusted node's attestation, recorded at receive time, that
// it added a specific Authentication-Results header while handling the message
// on a specific connection. Proofs are hash-linked in traversal order, so
// reordering, truncating, or editing the chain is detectable.
type HopProof struct {
	NodeID       string
	ConnectionID string
	HeaderHash   [32]byte
	Methods      []string
	Prev         [32]byte
	Hash         [32]byte
}

// Chain accumulates hop proofs for one receiving connection. A Chain must be
// created fresh per connection; proofs are bound to the connection ID and
// must never be reused for another connection, even for the same message.
type Chain struct {
	connID string
	hops   []HopProof
	tail   [32]byte
}

// NewChain starts a proof chain for the given receiving connection. The
// connection ID must be non-empty and unique per connection.
func NewChain(connectionID string) *Chain {
	return &Chain{connID: connectionID}
}

// ConnectionID returns the connection this chain is bound to.
func (c *Chain) ConnectionID() string { return c.connID }

// Attest records that a trusted node added rawHeader while evaluating the
// given authentication methods, and returns the recorded proof. It must be
// called in the order the message traversed the trusted nodes, outermost
// (border) hop first.
func (c *Chain) Attest(nodeID, rawHeader string, methods ...string) HopProof {
	p := HopProof{
		NodeID:       nodeID,
		ConnectionID: c.connID,
		HeaderHash:   sha256.Sum256([]byte(rawHeader)),
		Methods:      make([]string, len(methods)),
		Prev:         c.tail,
	}
	for i, m := range methods {
		p.Methods[i] = strings.ToLower(m)
	}
	p.Hash = computeHash(p)
	c.tail = p.Hash
	c.hops = append(c.hops, p)
	return p
}

// Proofs returns a copy of the recorded proofs in traversal order.
func (c *Chain) Proofs() []HopProof {
	return append([]HopProof(nil), c.hops...)
}

// Verify checks the internal integrity of the chain: every proof must be
// bound to this chain's connection and hash-linked to its predecessor.
func (c *Chain) Verify() error {
	valid, reason := validateProofs(c.hops, c.connID)
	for i := range valid {
		if !valid[i] {
			return fmt.Errorf("netsec: hop %d: %s", i, reason[i])
		}
	}
	return nil
}

func computeHash(p HopProof) [32]byte {
	h := sha256.New()
	h.Write(p.Prev[:])
	h.Write([]byte(p.ConnectionID))
	h.Write([]byte{0})
	h.Write([]byte(p.NodeID))
	h.Write([]byte{0})
	h.Write(p.HeaderHash[:])
	for _, m := range p.Methods {
		h.Write([]byte{0})
		h.Write([]byte(strings.ToLower(m)))
	}
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum
}

// validateProofs recomputes the hash chain and reports which proofs are
// intact and bound to connID. A failed proof invalidates every later proof,
// since they all chain onto a bad base.
func validateProofs(proofs []HopProof, connID string) (valid []bool, reason []string) {
	valid = make([]bool, len(proofs))
	reason = make([]string, len(proofs))
	var prev [32]byte
	chainOK := true
	for i, p := range proofs {
		switch {
		case p.ConnectionID != connID:
			reason[i] = ReasonProofConnection
		case !chainOK || p.Prev != prev || computeHash(p) != p.Hash:
			reason[i] = ReasonProofTampered
		default:
			valid[i] = true
		}
		if !valid[i] {
			chainOK = false
		}
		prev = p.Hash
	}
	return valid, reason
}
