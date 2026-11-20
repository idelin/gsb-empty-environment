package bootpolicy

// BuildPartition constructs the finite equivalence partition of the space of
// possible load events induced by the union of the original policy entries and
// the host-supplied compression candidates. originalCount is the number of
// leading entries belonging to the original policy.
//
// Every region of the partition has constant admission decisions under every
// involved entry, so enumerating one representative per region is a complete
// (never sampled) equivalence argument.
func BuildPartition(entries []Entry, originalCount int, cellLimit int) (*Partition, error) {
	if cellLimit <= 0 {
		cellLimit = MaxPartitionCells
	}
	for i := range entries {
		if err := entries[i].Validate(); err != nil {
			return nil, err
		}
	}
	part := &Partition{entries: entries}
	cert := &part.cert

	prefixes, prefixClasses := buildPrefixClasses(entries)
	cert.Prefixes = prefixes
	cert.PrefixClasses = prefixClasses
	signerClasses := buildSignerClasses(entries)
	cert.GenClasses = buildGenClasses(entries)
	cert.LocationClasses = buildLocationClasses(entries)
	cert.ParentClasses = buildParentClasses(entries)

	hasExempt := false
	for i := range entries {
		if entries[i].Kind == KindExempt {
			hasExempt = true
			break
		}
	}
	if hasExempt {
		cert.DigestClasses = buildDigestClasses(entries)
		cert.RootClasses = buildRootClasses(entries)
		cert.TimeClasses = buildTimeClasses(entries)
	}
	cert.OriginalEntryCount = originalCount

	type groupKey struct {
		denyMask   uint64
		exemptMask uint64
		origDenied bool
	}
	groupIndex := map[groupKey]int{}
	visit := func(c cell) {
		key := groupKey{denyMask: c.denyMask, exemptMask: c.exemptMask}
		key.origDenied = decideDenied(
			key.denyMask&originalMask(originalCount),
			key.exemptMask&originalMask(originalCount),
		)
		if _, ok := groupIndex[key]; ok {
			return
		}
		g := cellGroup{
			denyMask:   key.denyMask,
			exemptMask: key.exemptMask,
			origDenied: key.origDenied,
			event:      c.event,
		}
		groupIndex[key] = len(part.groups)
		part.groups = append(part.groups, g)
	}

	enum := cellEnumerator{
		entries:   entries,
		signers:   signerClasses,
		prefixes:  prefixClasses,
		gens:      cert.GenClasses,
		locations: cert.LocationClasses,
		digests:   cert.DigestClasses,
		roots:     cert.RootClasses,
		times:     cert.TimeClasses,
		parents:   cert.ParentClasses,
		cellLimit: cellLimit,
		visit:     visit,
	}
	if err := enum.run(); err != nil {
		return nil, err
	}

	cert.GroupDenyMasks = make([]uint64, len(part.groups))
	cert.GroupExemptMasks = make([]uint64, len(part.groups))
	cert.GroupOrigDenied = make([]bool, len(part.groups))
	for i, g := range part.groups {
		cert.GroupDenyMasks[i] = g.denyMask
		cert.GroupExemptMasks[i] = g.exemptMask
		cert.GroupOrigDenied[i] = g.origDenied
	}
	return part, nil
}

func originalMask(originalCount int) uint64 {
	if originalCount >= 64 {
		return ^uint64(0)
	}
	return uint64(1)<<uint(originalCount) - 1
}

type cellEnumerator struct {
	entries   []Entry
	signers   []ValueClass
	prefixes  []PrefixClass
	gens      []IntervalClass
	locations []ValueClass
	digests   []DigestClass
	roots     []ValueClass
	times     []TimeClass
	parents   []ParentClass
	cellLimit int
	count     int
	visit     func(cell)
}

func (e *cellEnumerator) run() error {
	var ev LoadEvent
	return e.dimSigner(ev)
}

func (e *cellEnumerator) tick() error {
	e.count++
	if e.count > e.cellLimit {
		return ErrPartitionTooLarge
	}
	return nil
}

func (e *cellEnumerator) dimSigner(ev LoadEvent) error {
	for i := range e.signers {
		next := ev
		next.Child.SignerID = e.signers[i].Representative
		if err := e.dimLabel(next); err != nil {
			return err
		}
	}
	return nil
}

func (e *cellEnumerator) dimLabel(ev LoadEvent) error {
	for i := range e.prefixes {
		next := ev
		next.Child.Label = e.prefixes[i].Representative
		if err := e.dimGen(next); err != nil {
			return err
		}
	}
	return nil
}

func (e *cellEnumerator) dimGen(ev LoadEvent) error {
	for i := range e.gens {
		next := ev
		next.Child.Generation = e.gens[i].Representative
		if err := e.dimLocation(next); err != nil {
			return err
		}
	}
	return nil
}

func (e *cellEnumerator) dimLocation(ev LoadEvent) error {
	for i := range e.locations {
		next := ev
		if e.locations[i].Wildcard {
			next.Child.Location = wildcardLocation()
		} else {
			next.Child.Location = e.locations[i].Representative
		}
		if err := e.dimDigest(next); err != nil {
			return err
		}
	}
	return nil
}

func (e *cellEnumerator) dimDigest(ev LoadEvent) error {
	if len(e.digests) == 0 {
		return e.dimRoot(ev)
	}
	for i := range e.digests {
		next := ev
		if e.digests[i].Wildcard {
			next.Child.Digest = wildcardDigest(e.digests)
		} else {
			next.Child.Digest = e.digests[i].Representative
		}
		if err := e.dimRoot(next); err != nil {
			return err
		}
	}
	return nil
}

func (e *cellEnumerator) dimRoot(ev LoadEvent) error {
	if len(e.roots) == 0 {
		return e.dimTime(ev)
	}
	for i := range e.roots {
		next := ev
		if e.roots[i].Wildcard {
			next.Child.RootID = wildcardRoot(e.roots)
		} else {
			next.Child.RootID = e.roots[i].Representative
		}
		if err := e.dimTime(next); err != nil {
			return err
		}
	}
	return nil
}

func (e *cellEnumerator) dimTime(ev LoadEvent) error {
	if len(e.times) == 0 {
		return e.dimParent(ev)
	}
	for i := range e.times {
		next := ev
		next.BootTime = e.times[i].Representative
		if err := e.dimParent(next); err != nil {
			return err
		}
	}
	return nil
}

func (e *cellEnumerator) dimParent(ev LoadEvent) error {
	for i := range e.parents {
		next := ev
		pc := &e.parents[i]
		switch {
		case pc.NilParent:
			next.Parent = nil
		case pc.Wildcard:
			parent := wildcardParent()
			next.Parent = &parent
		default:
			parent := Component{
				SignerID: pc.Scope.SignerID,
				Label:    pc.Scope.Label,
				Digest:   Digest{Algorithm: "parent", Hash: []byte{1}},
			}
			next.Parent = &parent
		}
		if err := e.tick(); err != nil {
			return err
		}
		e.visit(e.buildCell(next))
	}
	return nil
}

func (e *cellEnumerator) buildCell(ev LoadEvent) cell {
	c := cell{event: ev}
	for i := range e.entries {
		if !e.entries[i].Matches(ev) {
			continue
		}
		bit := uint(i)
		if e.entries[i].Kind == KindDeny {
			c.denyMask |= 1 << bit
		} else {
			c.exemptMask |= 1 << bit
		}
	}
	return c
}

func wildcardLocation() string {
	return "\x00__bootpolicy_wildcard_location__"
}

func wildcardRoot(named []ValueClass) string {
	for candidate := "\x00root-other"; ; candidate += "x" {
		collides := false
		for i := range named {
			if named[i].Representative == candidate {
				collides = true
				break
			}
		}
		if !collides {
			return candidate
		}
	}
}

func wildcardDigest(named []DigestClass) Digest {
	base := Digest{Algorithm: "\x00bootpolicy-other", Hash: []byte{0xFF}}
	for attempt := 0; ; attempt++ {
		candidate := Digest{Algorithm: base.Algorithm, Hash: append(append([]byte(nil), base.Hash...), byte(attempt))}
		collides := false
		for i := range named {
			if named[i].Representative.equal(candidate) {
				collides = true
				break
			}
		}
		if !collides {
			return candidate
		}
	}
}

func wildcardParent() Component {
	return Component{
		SignerID: "\x00__bootpolicy_wildcard_parent_signer__",
		Label:    "\x00__bootpolicy_wildcard_parent_label__",
		Digest:   Digest{Algorithm: "parent", Hash: []byte{2}},
	}
}
