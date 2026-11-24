package capacitycred

import (
	"context"
	"sort"
)

type credential struct {
	id         CredID
	root       CredID
	tenant     TenantID
	total      uint64
	interval   Interval
	commitment []byte
	owner      NodeID
	parent     CredID
	children   []CredID
	conflicted bool
	forked     bool
	revoked    bool
	chainTip   EventID
}

type kernel struct {
	creds     map[CredID]*credential
	events    map[EventID]Event
	order     []EventID
	pending   map[EventID]Event
	receipts  map[CredID]Receipt
	conflicts map[CredID][]EventID
	holds     map[CredID]uint64
}

func newKernel(cb Callbacks) *kernel {
	return &kernel{
		creds:     make(map[CredID]*credential),
		events:    make(map[EventID]Event),
		pending:   make(map[EventID]Event),
		receipts:  make(map[CredID]Receipt),
		conflicts: make(map[CredID][]EventID),
		holds:     make(map[CredID]uint64),
	}
}

// ingest authenticates one event and merges everything currently resolvable.
// Events whose causal predecessor has not arrived stay buffered.
func (k *kernel) ingest(cb Callbacks, e Event) error {
	if err := cb.valid(); err != nil {
		return err
	}
	if err := validateShape(e); err != nil {
		return err
	}
	body := eventBody(e)
	digest, err := cb.Hash(context.Background(), body)
	if err != nil {
		return err
	}
	if EventID(digest) != e.EventID {
		return ErrBadEventID
	}
	if err := cb.Verify(e.Tenant, body, e.Signature); err != nil {
		return ErrBadSignature
	}
	if previous, ok := k.events[e.EventID]; ok {
		if !sameEvent(previous, e) {
			return ErrDuplicateEvent
		}
		return nil
	}
	if _, queued := k.pending[e.EventID]; queued {
		return nil
	}
	k.pending[e.EventID] = e
	return k.drain()
}

// drain repeatedly applies ready events in a stable id-sorted order so the
// final state never depends on arrival order.
func (k *kernel) drain() error {
	for len(k.pending) > 0 {
		ids := make([]EventID, 0, len(k.pending))
		for id := range k.pending {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		progress := false
		for _, id := range ids {
			e := k.pending[id]
			if !k.ready(e) {
				continue
			}
			if err := k.apply(e); err != nil {
				delete(k.pending, id)
				return err
			}
			delete(k.pending, id)
			k.events[id] = e
			k.order = append(k.order, id)
			progress = true
		}
		if !progress {
			return nil
		}
	}
	return nil
}

func (k *kernel) ready(e Event) bool {
	if e.Kind == KindMint {
		return true
	}
	if _, ok := k.creds[e.CredID]; !ok {
		return false
	}
	if e.Prev != "" {
		if _, ok := k.events[e.Prev]; !ok {
			return false
		}
	}
	return true
}

func (k *kernel) apply(e Event) error {
	switch e.Kind {
	case KindMint:
		return k.applyMint(e)
	case KindSplit:
		return k.applySplit(e)
	case KindHandoff:
		return k.applyHandoff(e)
	case KindRevoke:
		return k.applyRevoke(e)
	default:
		return ErrInvalidEvent
	}
}

func (k *kernel) applyMint(e Event) error {
	if e.ParentID != "" || e.Range.Low != 0 || e.Range.High != e.Total ||
		e.Total == 0 || len(e.ParamsCommitment) == 0 || e.From == "" ||
		len(e.Children) != 0 || e.Prev != "" {
		return ErrInvalidEvent
	}
	if _, exists := k.creds[e.CredID]; exists {
		return ErrDuplicateEvent
	}
	k.creds[e.CredID] = &credential{
		id:         e.CredID,
		root:       e.CredID,
		tenant:     e.Tenant,
		total:      e.Total,
		interval:   e.Range,
		commitment: append([]byte(nil), e.ParamsCommitment...),
		owner:      e.From,
		chainTip:   e.EventID,
	}
	return nil
}

func (k *kernel) applySplit(e Event) error {
	parent, ok := k.creds[e.CredID]
	if !ok {
		return ErrUnknownCred
	}
	if parent.tenant != e.Tenant || e.ParentID != parent.root {
		return ErrTenantMismatch
	}
	if parent.conflicted || parent.forked || parent.revoked {
		return ErrBlocked
	}
	if k.holds[parent.id] != 0 {
		return ErrEncumbered
	}
	if err := validatePartition(parent, e.Children, k.creds); err != nil {
		return err
	}

	forked := e.Prev != "" && e.Prev != parent.chainTip
	conflict := forked
	existingChildren := append([]CredID(nil), parent.children...)
	for _, spec := range e.Children {
		for _, siblingID := range existingChildren {
			if siblingID == spec.ID {
				continue
			}
			if k.creds[siblingID].interval.overlaps(spec.Range) {
				conflict = true
			}
		}
	}

	for _, spec := range e.Children {
		if _, exists := k.creds[spec.ID]; !exists {
			k.creds[spec.ID] = &credential{
				id:         spec.ID,
				root:       parent.root,
				tenant:     parent.tenant,
				total:      spec.Range.High - spec.Range.Low,
				interval:   spec.Range,
				commitment: parent.commitment,
				owner:      parent.owner,
				parent:     parent.id,
				chainTip:   e.EventID,
			}
			parent.children = append(parent.children, spec.ID)
		}
	}

	if conflict {
		if forked {
			parent.forked = true
		}
		k.markConflict(parent.id, e.EventID)
		if e.Prev != "" {
			k.markConflict(parent.id, e.Prev)
		}
		for _, spec := range e.Children {
			for _, siblingID := range existingChildren {
				if siblingID == spec.ID {
					continue
				}
				sibling := k.creds[siblingID]
				if sibling.interval.overlaps(spec.Range) {
					sibling.conflicted = true
					k.creds[spec.ID].conflicted = true
					k.markConflict(sibling.id, e.EventID)
					k.markConflict(sibling.id, parent.chainTip)
					k.markConflict(k.creds[spec.ID].id, e.EventID)
				}
			}
		}
	}
	parent.chainTip = e.EventID
	return nil
}

func (k *kernel) applyHandoff(e Event) error {
	cred, ok := k.creds[e.CredID]
	if !ok {
		return ErrUnknownCred
	}
	if cred.tenant != e.Tenant || e.ParentID != cred.root {
		return ErrTenantMismatch
	}
	if cred.conflicted || cred.revoked {
		return ErrBlocked
	}
	if e.To == "" || e.From == "" || e.To == e.From {
		return ErrInvalidEvent
	}
	if k.subtreeHolds(cred) != 0 {
		return ErrEncumbered
	}
	if e.Prev != "" && e.Prev != cred.chainTip {
		// Two signed handoffs fork the same credential. Even though both
		// signatures verify, the interval must not be spendable in two places.
		cred.conflicted = true
		cred.owner = ""
		k.markConflict(cred.id, e.EventID)
		k.markConflict(cred.id, cred.chainTip)
		cred.chainTip = e.EventID
		return nil
	}
	if cred.owner != e.From {
		return ErrWrongOwner
	}
	cred.owner = e.To
	cred.chainTip = e.EventID
	return nil
}

func (k *kernel) applyRevoke(e Event) error {
	cred, ok := k.creds[e.CredID]
	if !ok {
		return ErrUnknownCred
	}
	if cred.tenant != e.Tenant || e.ParentID != cred.root {
		return ErrTenantMismatch
	}
	if e.Prev != "" && e.Prev != cred.chainTip {
		return ErrInvalidEvent
	}
	cred.revoked = true
	cred.chainTip = e.EventID
	return nil
}

func (k *kernel) markConflict(id CredID, evidence EventID) {
	for _, existing := range k.conflicts[id] {
		if existing == evidence {
			return
		}
	}
	k.conflicts[id] = append(k.conflicts[id], evidence)
}

func (k *kernel) subtreeHolds(root *credential) uint64 {
	total := k.holds[root.id]
	for _, childID := range root.children {
		total += k.subtreeHolds(k.creds[childID])
	}
	return total
}

func (k *kernel) blocked(id CredID) bool {
	for cur := k.creds[id]; cur != nil; {
		if cur.conflicted || cur.revoked {
			return true
		}
		if cur.parent == "" {
			return false
		}
		cur = k.creds[cur.parent]
	}
	return false
}

func (k *kernel) freeBytes(id CredID) (uint64, bool) {
	cred := k.creds[id]
	if k.blocked(id) {
		return 0, false
	}
	capacity, err := checkedSub(cred.interval.High, cred.interval.Low)
	if err != nil {
		return 0, false
	}
	free, err := checkedSub(capacity, k.holds[id])
	if err != nil {
		return 0, false
	}
	return free, true
}
