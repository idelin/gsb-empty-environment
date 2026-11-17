package keyref

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
)

var (
	ErrAliasNotFound   = errors.New("keyref: alias not found")
	ErrKeyNotFound     = errors.New("keyref: key not found")
	ErrAliasCycle      = errors.New("keyref: alias cycle detected")
	ErrCrossAccount    = errors.New("keyref: alias chain crosses into an unauthorized account")
	ErrMaxHopsExceeded = errors.New("keyref: alias chain exceeds maximum hops")
)

// Policy constrains alias-chain resolution.
type Policy struct {
	// Grants maps an account to the set of accounts its aliases may
	// delegate to. A hop from account A to a different account B is
	// rejected unless B is in Grants[A]. Same-account hops and hops to
	// concrete keys always stay within the referring alias's account.
	Grants map[string][]string
	// MaxHops bounds chain length; zero uses DefaultMaxHops.
	MaxHops int
}

// DefaultMaxHops is the default chain-length bound.
const DefaultMaxHops = 8

func (p Policy) allows(from, to string) bool {
	if from == to {
		return true
	}
	for _, acct := range p.Grants[from] {
		if acct == to {
			return true
		}
	}
	return false
}

// Hop records one step of a resolved alias chain for auditing.
type Hop struct {
	Ref     Ref
	Version int64
}

// ResolvedRef is the fully resolved identity of an alias reference: the
// account and region that own the terminal key, the version of the
// first-hop alias binding, and the final public key fingerprint.
type ResolvedRef struct {
	Account      string
	Region       string
	Alias        string
	AliasVersion int64
	KeyID        string
	KeyVersion   int64
	Fingerprint  string
	Chain        []Hop
}

// Fingerprint computes the canonical fingerprint of public key material:
// hex-encoded SHA-256 over the PKIX DER bytes.
func Fingerprint(publicKeyDER []byte) string {
	sum := sha256.Sum256(publicKeyDER)
	return hex.EncodeToString(sum[:])
}

// Resolver resolves alias references against a Store under a Policy.
type Resolver struct {
	store  *Store
	policy Policy
}

func NewResolver(store *Store, policy Policy) *Resolver {
	if policy.MaxHops <= 0 {
		policy.MaxHops = DefaultMaxHops
	}
	return &Resolver{store: store, policy: policy}
}

// Resolve follows the alias chain starting at ref until it reaches concrete
// key material. Every hop is read through tx, so the whole chain is pinned
// to the transaction snapshot. Chains may span multiple hops but may not
// cross into an account the referring account is not authorized for.
func (r *Resolver) Resolve(tx *Tx, ref Ref) (ResolvedRef, error) {
	visited := make(map[Ref]struct{})
	chain := make([]Hop, 0, 4)
	current := ref

	for hop := 0; ; hop++ {
		if hop >= r.policy.MaxHops {
			return ResolvedRef{}, fmt.Errorf("%w: limit %d at %s/%s/%s",
				ErrMaxHopsExceeded, r.policy.MaxHops, current.Account, current.Region, current.Alias)
		}
		if _, seen := visited[current]; seen {
			return ResolvedRef{}, fmt.Errorf("%w: %s/%s/%s",
				ErrAliasCycle, current.Account, current.Region, current.Alias)
		}
		visited[current] = struct{}{}

		binding, ok := tx.GetAlias(current)
		if !ok {
			return ResolvedRef{}, fmt.Errorf("%w: %s/%s/%s",
				ErrAliasNotFound, current.Account, current.Region, current.Alias)
		}
		chain = append(chain, Hop{Ref: current, Version: binding.Version})

		switch {
		case binding.Target.Alias != nil:
			next := *binding.Target.Alias
			if !r.policy.allows(current.Account, next.Account) {
				return ResolvedRef{}, fmt.Errorf("%w: %s may not delegate to account %s",
					ErrCrossAccount, current.Account, next.Account)
			}
			current = next
		case binding.Target.KeyID != "":
			key, ok := tx.GetKey(current.Account, current.Region, binding.Target.KeyID)
			if !ok {
				return ResolvedRef{}, fmt.Errorf("%w: %s/%s/%s",
					ErrKeyNotFound, current.Account, current.Region, binding.Target.KeyID)
			}
			return ResolvedRef{
				Account:      key.Account,
				Region:       key.Region,
				Alias:        ref.Alias,
				AliasVersion: chain[0].Version,
				KeyID:        key.KeyID,
				KeyVersion:   key.Version,
				Fingerprint:  Fingerprint(key.PublicKey),
				Chain:        chain,
			}, nil
		default:
			return ResolvedRef{}, fmt.Errorf("%w: %s/%s/%s has empty target",
				ErrAliasNotFound, current.Account, current.Region, current.Alias)
		}
	}
}
