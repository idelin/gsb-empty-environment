package netsec

import "time"

const (
	// MaxDelegationDepth is the maximum number of PKEY delegations that may be
	// followed while resolving one name. The initial ego zone is depth zero,
	// so the eighth delegation is allowed and a ninth fails.
	MaxDelegationDepth = 8
	// MaxCNAMERedirects bounds how many times a CNAME may restart resolution
	// before it is treated as non-terminating.
	MaxCNAMERedirects = 8
)

// Resolver resolves GNS names against a local ego table and a zone store.
// The zero value is not usable; construct one with NewResolver.
type Resolver struct {
	egos  *EgoTable
	store ZoneStore
	// now returns the current instant; tests inject a fixed clock.
	now func() time.Time
}

// NewResolver builds a resolver rooted at the local ego table. now may be nil,
// in which case time.Now is used.
func NewResolver(egos *EgoTable, store ZoneStore, now func() time.Time) *Resolver {
	if now == nil {
		now = time.Now
	}
	return &Resolver{egos: egos, store: store, now: now}
}

// Result is the outcome of a successful resolution.
type Result struct {
	// Name is the name that was resolved (the final alias after CNAME chains).
	Name string
	// Zone is the zone that owns the terminal label.
	Zone ZonePublicKey
	// Chain lists every zone key visited, starting with the ego zone and
	// ending with Zone. Its length minus one is the delegation depth.
	Chain []ZonePublicKey
}

// splitLabels splits a name into its labels from right to left. A single
// trailing dot is trimmed; empty labels and empty names are rejected.
func splitLabels(name string) ([]string, error) {
	if name == "" {
		return nil, ErrInvalidName
	}
	if len(name) > 1 && name[len(name)-1] == '.' {
		name = name[:len(name)-1]
		if name == "" {
			return nil, ErrInvalidName
		}
	}
	labels := make([]string, 0, 4)
	start := 0
	for i := 0; i <= len(name); i++ {
		if i == len(name) || name[i] == '.' {
			if i == start {
				return nil, ErrInvalidName
			}
			labels = append(labels, name[start:i])
			start = i + 1
		}
	}
	// Reverse: right-most label first.
	for left, right := 0, len(labels)-1; left < right; left, right = left+1, right-1 {
		labels[left], labels[right] = labels[right], labels[left]
	}
	return labels, nil
}

// Resolve walks name from right to left. The right-most label is resolved
// exclusively through the local ego table; every remaining label is looked up
// in the current zone, with PKEY records switching zones and CNAME records
// restarting under an alias.
func (r *Resolver) Resolve(name string) (*Result, error) {
	if r == nil || r.egos == nil || r.store == nil {
		return nil, ErrInvalidName
	}
	redirects := 0
	for {
		labels, err := splitLabels(name)
		if err != nil {
			return nil, err
		}

		// The root label may only ever come from the local ego table.
		rootKey, ok := r.egos.Lookup(labels[0])
		if !ok {
			return nil, ErrEgoNotFound
		}

		chain := []ZonePublicKey{rootKey}
		currentKey := rootKey

		if len(labels) == 1 {
			return &Result{Name: name, Zone: currentKey, Chain: chain}, nil
		}

		terminated := false
		restarted := false
		for _, label := range labels[1:] {
			zone, ok := r.store.Zone(currentKey)
			if !ok {
				return nil, ErrZoneNotFound
			}

			block, ok := zone.Records[label]
			if !ok {
				return nil, ErrLabelNotFound
			}

			now := r.now()
			var pkey, cname *Record
			liveRecords := 0
			for i := range block {
				rec := &block[i]
				// Absolute expiration at or before now: skip the record.
				if rec.expiredAt(now) {
					continue
				}
				liveRecords++
				switch rec.Type {
				case RecordPKEY:
					pkey = rec
				case RecordCNAME:
					cname = rec
				}
			}

			// A live PKEY and a live CNAME under one label invalidate the
			// entire block, regardless of their order.
			if pkey != nil && cname != nil {
				return nil, ErrConflictingRecords
			}

			if pkey != nil {
				nextKey := ZonePublicKey{Key: pkey.Value}
				if nextKey.IsZero() {
					return nil, ErrInvalidRecord
				}
				// A repeated zone key is a ring. Check the ring before the
				// depth budget so an over-deep chain that loops still reports
				// the loop.
				for _, seen := range chain {
					if seen.Equal(nextKey) {
						return nil, ErrDelegationLoop
					}
				}
				if len(chain)-1 >= MaxDelegationDepth {
					return nil, ErrDelegationTooDeep
				}
				currentKey = nextKey
				chain = append(chain, nextKey)
				continue
			}

			if cname != nil {
				alias := string(cname.Value)
				if alias == "" {
					return nil, ErrInvalidRecord
				}
				redirects++
				if redirects > MaxCNAMERedirects {
					return nil, ErrTooManyRedirects
				}
				name = alias
				restarted = true
				break
			}

			if liveRecords == 0 {
				// Every record under the label is expired: there is nothing
				// left to answer with.
				return nil, ErrLabelNotFound
			}

			// No steering record: this label is terminal data inside the
			// current zone and resolution ends here.
			terminated = true
			break
		}

		if terminated {
			return &Result{Name: name, Zone: currentKey, Chain: chain}, nil
		}
		if !restarted {
			// Every label was consumed by PKEY delegation: currentKey is the
			// apex zone authoritative for the name.
			return &Result{Name: name, Zone: currentKey, Chain: chain}, nil
		}
		// A CNAME restarted the walk with the alias name.
	}
}
