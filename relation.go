package netsec

import "fmt"

// Relation is the relationship of the current session neighbor to the local
// AS, as configured by the host.
type Relation uint8

const (
	RelationUnknown Relation = iota
	// Customer means the neighbor is a customer of the local AS.
	Customer
	// Provider means the neighbor is a provider of the local AS.
	Provider
	// Peer means the neighbor is a lateral peer of the local AS.
	Peer
)

func (r Relation) String() string {
	switch r {
	case Customer:
		return "customer"
	case Provider:
		return "provider"
	case Peer:
		return "peer"
	default:
		return "unknown"
	}
}

func (r Relation) valid() bool {
	return r == Customer || r == Provider || r == Peer
}

// Session identifies the eBGP session on which a route was received. LocalASN
// is intentionally not part of AS_PATH; NeighborASN is the most recently added
// AS in the path.
type Session struct {
	LocalASN    ASN
	NeighborASN ASN
	Relation    Relation
}

// Validate checks the host-provided session identity and relationship.
func (s Session) Validate() error {
	if err := ValidateASN(s.LocalASN); err != nil {
		return fmt.Errorf("local AS: %w", err)
	}
	if err := ValidateASN(s.NeighborASN); err != nil {
		return fmt.Errorf("neighbor AS: %w", err)
	}
	if s.LocalASN == s.NeighborASN {
		return fmt.Errorf("%w: local and neighbor AS are both %d", ErrInvalidSession, uint32(s.LocalASN))
	}
	if !s.Relation.valid() {
		return fmt.Errorf("%w: %s", ErrInvalidSession, s.Relation)
	}
	return nil
}
