package capacitycred

import "sort"

func validateShape(e Event) error {
	if e.Tenant == "" || e.CredID == "" || e.EventID == "" || e.Signature == nil {
		return ErrInvalidEvent
	}
	switch e.Kind {
	case KindMint, KindSplit, KindHandoff, KindRevoke:
		return nil
	default:
		return ErrInvalidEvent
	}
}

func validatePartition(parent *credential, children []ChildSpec, existing map[CredID]*credential) error {
	if len(children) == 0 {
		return ErrInvalidEvent
	}
	sorted := append([]ChildSpec(nil), children...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Range.Low < sorted[j].Range.Low })
	seen := make(map[CredID]bool, len(sorted))
	if sorted[0].Range.Low != parent.interval.Low {
		return ErrInvalidEvent
	}
	for idx, spec := range sorted {
		if !spec.Range.valid() || spec.ID == "" || seen[spec.ID] {
			return ErrInvalidEvent
		}
		seen[spec.ID] = true
		if prior, exists := existing[spec.ID]; exists {
			if prior.interval != spec.Range {
				return ErrDuplicateEvent
			}
		}
		if spec.Range.High <= spec.Range.Low || spec.Range.High > parent.interval.High {
			return ErrInvalidEvent
		}
		if idx > 0 && spec.Range.Low != sorted[idx-1].Range.High {
			return ErrInvalidEvent
		}
	}
	if sorted[len(sorted)-1].Range.High != parent.interval.High {
		return ErrInvalidEvent
	}
	return nil
}

func sameEvent(a, b Event) bool {
	return string(a.EventID) == string(b.EventID) && string(a.Signature) == string(b.Signature)
}

func (k *kernel) snapshot(local NodeID) *Snapshot {
	ids := make([]CredID, 0, len(k.creds))
	for id := range k.creds {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	views := make([]CredView, 0, len(ids))
	for _, id := range ids {
		cred := k.creds[id]
		held := k.holds[id]
		free, _ := k.freeBytes(id)
		children := append([]CredID(nil), cred.children...)
		sort.Slice(children, func(i, j int) bool { return children[i] < children[j] })
		_, hasReceipt := k.receipts[cred.root]
		views = append(views, CredView{
			ID:               cred.id,
			Root:             cred.root,
			Tenant:           cred.tenant,
			Total:            cred.total,
			Range:            cred.interval,
			ParamsCommitment: append([]byte(nil), cred.commitment...),
			Owner:            cred.owner,
			Parent:           cred.parent,
			Children:         children,
			Quarantined:      k.blocked(cred.id),
			Revoked:          cred.revoked,
			HeldBytes:        held,
			FreeBytes:        free,
			Runnable:         !k.blocked(cred.id) && hasReceipt,
			LocalOwner:       cred.owner == local,
		})
	}

	sequence := append([]EventID(nil), k.order...)
	sort.Slice(sequence, func(i, j int) bool { return sequence[i] < sequence[j] })

	conflicts := make(map[CredID][]EventID, len(k.conflicts))
	for id, evidence := range k.conflicts {
		ev := append([]EventID(nil), evidence...)
		sort.Slice(ev, func(i, j int) bool { return ev[i] < ev[j] })
		conflicts[id] = ev
	}
	roots := make(map[CredID]Receipt, len(k.receipts))
	for id, receipt := range k.receipts {
		roots[id] = receipt
	}
	return &Snapshot{
		Credentials:    views,
		FinalizedRoots: roots,
		Sequence:       sequence,
		Conflicts:      conflicts,
	}
}

// acceptReceipt validates a finality receipt. The receipt digest must equal
// hash(root || tip) where tip is the current chain tip of the root, and the
// signature must verify against the root tenant.
func (k *kernel) acceptReceipt(cb Callbacks, r Receipt) error {
	if err := cb.valid(); err != nil {
		return err
	}
	root, ok := k.creds[r.RootCredID]
	if !ok || root.parent != "" {
		return ErrBadReceipt
	}
	if r.Tenant != root.tenant || r.PrefixTip == "" || r.Signature == nil {
		return ErrBadReceipt
	}
	body := receiptBody(r)
	if r.PrefixTip != root.chainTip {
		return ErrBadReceipt
	}
	if err := cb.Verify(r.Tenant, body, r.Signature); err != nil {
		return ErrBadReceipt
	}
	k.receipts[r.RootCredID] = Receipt{
		RootCredID: r.RootCredID,
		Tenant:     r.Tenant,
		PrefixTip:  root.chainTip,
		Signature:  append([]byte(nil), r.Signature...),
	}
	return nil
}

func receiptBody(r Receipt) []byte {
	b := new(canonicalBuilder)
	b.tag(1).id(string(r.RootCredID))
	b.tag(2).id(string(r.Tenant))
	b.tag(3).id(string(r.PrefixTip))
	return b.buf
}

// ReceiptSigningBody returns the canonical byte stream covered by a finality
// receipt signature.
func ReceiptSigningBody(r Receipt) []byte {
	return receiptBody(r)
}

// Recompute rebuilds state from the complete immutable event set using exactly
// the same apply rules as online ingestion. Every envelope carries its own
// callbacks; a missing or nil callback rejects the whole recomputation.
func Recompute(envelopes []Envelope, local NodeID) (*Snapshot, error) {
	if len(envelopes) == 0 {
		return nil, ErrInvalidEvent
	}
	events := make([]Event, 0, len(envelopes))
	byID := make(map[EventID]Callbacks, len(envelopes))
	for _, envelope := range envelopes {
		if err := envelope.Callbacks.valid(); err != nil {
			return nil, err
		}
		byID[envelope.Event.EventID] = envelope.Callbacks
		events = append(events, envelope.Event)
	}
	sort.Slice(events, func(i, j int) bool { return events[i].EventID < events[j].EventID })

	k := newKernel(envelopes[0].Callbacks)
	for _, e := range events {
		if err := k.ingest(byID[e.EventID], e); err != nil {
			return nil, err
		}
	}
	if len(k.pending) != 0 {
		return nil, ErrUnresolved
	}
	return k.snapshot(local), nil
}
