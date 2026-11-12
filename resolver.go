package netsec

import (
	"context"
	"sync"
)

// DefaultMaxHops bounds alias chain length, providing defense in depth
// against pathological chains even when they are acyclic.
const DefaultMaxHops = 64

// Resolver opens pinned resolution transactions against a HostStore.
type Resolver struct {
	store   HostStore
	maxHops int
}

// Option customizes a Resolver.
type Option func(*Resolver)

// WithMaxHops overrides DefaultMaxHops.
func WithMaxHops(hops int) Option {
	return func(r *Resolver) {
		if hops > 0 {
			r.maxHops = hops
		}
	}
}

// NewResolver creates a Resolver backed by store.
func NewResolver(store HostStore, options ...Option) *Resolver {
	r := &Resolver{store: store, maxHops: DefaultMaxHops}
	for _, option := range options {
		option(r)
	}
	return r
}

// Txn is one resolution transaction. Every Resolve call observes the same
// immutable snapshot and the same per-reference cached outcome, so all
// resolution results in the transaction are fixed even if the host is
// rebound concurrently. A Txn is safe for concurrent use.
type Txn struct {
	resolver *Resolver
	caller   CallerIdentity
	snap     StoreSnapshot

	mu     sync.Mutex
	cached map[AliasRef]cachedResolution
}

type cachedResolution struct {
	res Resolution
	err error
}

// Begin opens a transaction bound to caller's authorization scope.
func (r *Resolver) Begin(ctx context.Context, caller CallerIdentity) (*Txn, error) {
	snap, err := r.store.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	return &Txn{
		resolver: r,
		caller:   caller,
		snap:     snap,
		cached:   make(map[AliasRef]cachedResolution),
	}, nil
}

// Resolve fully resolves ref within the transaction. The result is pinned:
// repeated calls return identical versions, hops, and fingerprints regardless
// of concurrent rebinding.
func (txn *Txn) Resolve(ctx context.Context, ref AliasRef) (Resolution, error) {
	if !ref.Valid() {
		return Resolution{}, ErrInvalidReference
	}

	txn.mu.Lock()
	defer txn.mu.Unlock()

	if pinned, ok := txn.cached[ref]; ok {
		return cloneResolution(pinned.res), pinned.err
	}

	res, err := txn.walk(ctx, ref)
	txn.cached[ref] = cachedResolution{res: cloneResolution(res), err: err}
	return res, err
}

func (txn *Txn) walk(ctx context.Context, entry AliasRef) (Resolution, error) {
	var (
		hops    []Hop
		path    = []AliasRef{entry}
		visited = map[AliasRef]int{entry: 0}
		current = entry
	)

	for {
		if err := ctx.Err(); err != nil {
			return Resolution{}, err
		}
		if len(hops) >= txn.resolver.maxHops {
			return Resolution{}, ErrMaxHops
		}
		if !txn.caller.AuthorizedTo(current.Account) {
			return Resolution{}, &AccountAuthError{Account: current.Account, Ref: current}
		}

		binding, ok := txn.snap.LookupAlias(current)
		if !ok {
			return Resolution{}, ErrAliasNotFound
		}
		if err := validateBinding(binding); err != nil {
			return Resolution{}, err
		}
		hops = append(hops, Hop{Ref: current, Version: binding.Version, Target: binding.Target})

		switch binding.Target.Kind {
		case TargetKey:
			pub, ok := txn.snap.LookupKey(current.Account, binding.Target.Key)
			if !ok {
				return Resolution{}, ErrKeyNotFound
			}
			if err := validateKey(pub); err != nil {
				return Resolution{}, err
			}
			return Resolution{
				Entry:            entry,
				EntryVersion:     hops[0].Version,
				FinalAccount:     pub.Account,
				FinalRegion:      pub.Region,
				FinalKey:         pub.ID,
				FinalFingerprint: pub.Fingerprint,
				Hops:             hops,
			}, nil

		case TargetAlias:
			next := *binding.Target.Alias
			if at, seen := visited[next]; seen {
				cyclePath := append(append([]AliasRef(nil), path...), next)
				return Resolution{}, &CycleError{Path: cyclePath[at:]}
			}
			visited[next] = len(path)
			path = append(path, next)
			current = next
		}
	}
}

func validateKey(pub PublicKey) error {
	if pub.Account == "" || pub.ID == "" || pub.Fingerprint == "" {
		return ErrInvalidKey
	}
	if len(pub.DER) > 0 && SHA256Fingerprint(pub.DER) != pub.Fingerprint {
		return ErrFingerprintMismatch
	}
	return nil
}

func cloneResolution(res Resolution) Resolution {
	if res.Hops != nil {
		res.Hops = append([]Hop(nil), res.Hops...)
		for i := range res.Hops {
			if res.Hops[i].Target.Alias != nil {
				alias := *res.Hops[i].Target.Alias
				res.Hops[i].Target.Alias = &alias
			}
		}
	}
	return res
}
