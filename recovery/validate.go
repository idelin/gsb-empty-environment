package recovery

import "fmt"

// Validate checks every structural and semantic precondition of a recovery
// problem, including that every candidate site is realizable by some serial
// order of its committed transactions.
func (p *Problem) Validate() error {
	if len(p.Txns) == 0 || len(p.Txns) > 10 {
		return fmt.Errorf("%w: need 1..10 transactions, got %d", ErrInvalidProblem, len(p.Txns))
	}
	if len(p.Sites) == 0 || len(p.Sites) > 12 {
		return fmt.Errorf("%w: need 1..12 sites, got %d", ErrInvalidProblem, len(p.Sites))
	}
	if len(p.Commands) == 0 && len(p.Probes) == 0 {
		return fmt.Errorf("%w: at least one command or probe is required", ErrInvalidProblem)
	}

	txnByID := make(map[TxnID]*Txn, len(p.Txns))
	groups := make(map[Group][]TxnID)
	keys := make(map[Key]struct{})
	for i := range p.Txns {
		t := &p.Txns[i]
		if _, dup := txnByID[t.ID]; dup {
			return fmt.Errorf("%w: duplicate txn id %d", ErrInvalidProblem, t.ID)
		}
		txnByID[t.ID] = t
		groups[t.Group] = append(groups[t.Group], t.ID)
		for _, r := range t.Reads {
			if r.Low > r.High {
				return fmt.Errorf("%w: read interval inverted in txn %d", ErrInvalidProblem, t.ID)
			}
			keys[r.Low] = struct{}{}
			keys[r.High] = struct{}{}
		}
		if t.HasWrite {
			keys[t.WriteKey] = struct{}{}
		}
	}
	if len(keys) > 6 {
		return fmt.Errorf("%w: more than six distinct keys: %d", ErrInvalidProblem, len(keys))
	}

	// Group membership is ordered by increasing ID; prefixes reference this
	// order, so each group must be internally contiguous after sorting.
	groupOrder := make(map[Group][]TxnID, len(groups))
	for g, ids := range groups {
		sortIDs(ids)
		for i := 1; i < len(ids); i++ {
			if ids[i] == ids[i-1] {
				return fmt.Errorf("%w: duplicate id in group %d", ErrInvalidProblem, g)
			}
		}
		groupOrder[g] = ids
	}

	allBits := BitsetAll(len(p.Txns))
	if p.TargetCommitted&^allBits != 0 {
		return fmt.Errorf("%w: target committed bitmap references unknown txn", ErrInvalidProblem)
	}
	if err := p.checkPrefixClosed(p.TargetCommitted, groupOrder, "target"); err != nil {
		return err
	}

	names := make(map[string]struct{})
	for i := range p.Commands {
		c := &p.Commands[i]
		if c.Cost <= 0 {
			return fmt.Errorf("%w: command %q must have positive cost", ErrInvalidProblem, c.Name)
		}
		if _, ok := groupOrder[c.Group]; !ok {
			return fmt.Errorf("%w: command %q references unknown group %d", ErrInvalidProblem, c.Name, c.Group)
		}
		if c.Prefix <= 0 || c.Prefix > len(groupOrder[c.Group]) {
			return fmt.Errorf("%w: command %q prefix out of range", ErrInvalidProblem, c.Name)
		}
		if _, dup := names[c.Name]; dup {
			return fmt.Errorf("%w: duplicate action name %q", ErrInvalidProblem, c.Name)
		}
		names[c.Name] = struct{}{}
	}
	for i := range p.Probes {
		pr := &p.Probes[i]
		if pr.Cost <= 0 {
			return fmt.Errorf("%w: probe %q must have positive cost", ErrInvalidProblem, pr.Name)
		}
		if _, ok := groupOrder[pr.Group]; !ok {
			return fmt.Errorf("%w: probe %q references unknown group %d", ErrInvalidProblem, pr.Name, pr.Group)
		}
		if _, dup := names[pr.Name]; dup {
			return fmt.Errorf("%w: duplicate action name %q", ErrInvalidProblem, pr.Name)
		}
		names[pr.Name] = struct{}{}
	}

	siteNames := make(map[string]struct{})
	for i := range p.Sites {
		s := &p.Sites[i]
		if s.Name == "" {
			return fmt.Errorf("%w: site %d has no name", ErrInvalidProblem, i)
		}
		if _, dup := siteNames[s.Name]; dup {
			return fmt.Errorf("%w: duplicate site name %q", ErrInvalidProblem, s.Name)
		}
		siteNames[s.Name] = struct{}{}
		if s.Committed&^allBits != 0 {
			return fmt.Errorf("%w: site %q bitmap references unknown txn", ErrInvalidProblem, s.Name)
		}
		if err := p.checkPrefixClosed(s.Committed, groupOrder, "site "+s.Name); err != nil {
			return err
		}
		if len(s.KV) > 6 {
			return fmt.Errorf("%w: site %q holds more than six keys", ErrInvalidProblem, s.Name)
		}
		if !p.realizable(*s, txnByID, groupOrder) {
			return fmt.Errorf("%w: site %q is not realizable by its committed transactions", ErrInvalidProblem, s.Name)
		}
	}
	if len(p.TargetKV) > 6 {
		return fmt.Errorf("%w: target holds more than six keys", ErrInvalidProblem)
	}
	return nil
}

func (p *Problem) checkPrefixClosed(bits Bitset, order map[Group][]TxnID, where string) error {
	for g, ids := range order {
		for i, id := range ids {
			if !bits.Contains(id) {
				continue
			}
			if i > 0 && !bits.Contains(ids[i-1]) {
				return fmt.Errorf("%w: %s commits txn %d without group %d predecessor",
					ErrInvalidProblem, where, id, g)
			}
		}
	}
	return nil
}

func sortIDs(ids []TxnID) {
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0 && ids[j-1] > ids[j]; j-- {
			ids[j-1], ids[j] = ids[j], ids[j-1]
		}
	}
}
