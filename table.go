package tokenmigrate

import (
	"sort"
	"time"
)

type readKey struct {
	tenant DomainID
	origin Scope
	token  Token
}

// ReadTable is an opaque compatibility index. Looking up a token outside the
// caller's authorized target domain returns ErrNotFound, identical to a token
// that does not exist.
type ReadTable struct {
	entries map[readKey]ReadResult
}

// CompatibilityEntries returns a sorted administrative copy of the table.
// Hosts may persist these rows, but must not expose them directly to tenants.
func (p *Plan) CompatibilityEntries() []ReadResult {
	if p == nil || p.table == nil {
		return nil
	}
	out := make([]ReadResult, 0, len(p.table.entries))
	for _, entry := range p.table.entries {
		out = append(out, cloneReadResult(entry))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Tenant != out[j].Tenant {
			return out[i].Tenant < out[j].Tenant
		}
		if out[i].Origin != out[j].Origin {
			if out[i].Origin.Domain != out[j].Origin.Domain {
				return out[i].Origin.Domain < out[j].Origin.Domain
			}
			return out[i].Origin.Generation < out[j].Origin.Generation
		}
		return out[i].OldToken < out[j].OldToken
	})
	return out
}

func buildCompatibilityTable(v *validatedInput, placements []placement) (*ReadTable, error) {
	table := &ReadTable{entries: make(map[readKey]ReadResult)}
	for i := range placements {
		p := placements[i]
		a := p.assignment
		if !a.changed() && a.Origin == a.Target {
			continue
		}
		r, ok := v.records[a.RecordID]
		if !ok {
			return nil, invalid("missing record %s while building compatibility table", a.RecordID)
		}
		entry := ReadResult{
			Tenant:     a.Target.Domain,
			Origin:     a.Origin,
			OldToken:   a.OldToken,
			Target:     a.Target,
			Token:      a.NewToken,
			Commitment: a.Commitment,
			Class:      a.Class,
			RecordIDs:  []RecordID{a.RecordID},
			ValidFrom:  v.at,
			ValidUntil: r.Valid.NotAfter,
		}
		if !r.Valid.NotBefore.IsZero() && r.Valid.NotBefore.After(entry.ValidFrom) {
			entry.ValidFrom = r.Valid.NotBefore
		}
		if p.change != nil && !p.change.Valid.NotAfter.IsZero() {
			if entry.ValidUntil.IsZero() || p.change.Valid.NotAfter.Before(entry.ValidUntil) {
				entry.ValidUntil = p.change.Valid.NotAfter
			}
		}

		key := readKey{tenant: a.Target.Domain, origin: a.Origin, token: a.OldToken}
		if previous, exists := table.entries[key]; exists {
			if previous.Target != entry.Target ||
				previous.Token != entry.Token ||
				previous.Class != entry.Class {
				return nil, invalid("compatibility key %s/%s/%s resolves ambiguously", a.Target.Domain, a.Origin, a.OldToken)
			}
			seen := make(map[RecordID]struct{}, len(previous.RecordIDs))
			for _, id := range previous.RecordIDs {
				seen[id] = struct{}{}
			}
			for _, id := range entry.RecordIDs {
				if _, duplicate := seen[id]; !duplicate {
					previous.RecordIDs = append(previous.RecordIDs, id)
				}
			}
			sort.Slice(previous.RecordIDs, func(i, j int) bool {
				return previous.RecordIDs[i] < previous.RecordIDs[j]
			})
			if !entry.ValidFrom.IsZero() && (previous.ValidFrom.IsZero() || entry.ValidFrom.Before(previous.ValidFrom)) {
				previous.ValidFrom = entry.ValidFrom
			}
			if entry.ValidUntil.IsZero() || (!previous.ValidUntil.IsZero() && entry.ValidUntil.After(previous.ValidUntil)) {
				previous.ValidUntil = entry.ValidUntil
			}
			table.entries[key] = previous
			continue
		}
		table.entries[key] = entry
	}
	return table, nil
}

func (t *ReadTable) lookup(req ReadRequest) (ReadResult, error) {
	if t == nil || req.Tenant == "" || req.Origin.Domain == "" || req.Token == "" || req.At.IsZero() {
		return ReadResult{}, ErrNotFound
	}
	entry, ok := t.entries[readKey{tenant: req.Tenant, origin: req.Origin, token: req.Token}]
	if !ok || !entry.visibleAt(req.At) {
		return ReadResult{}, ErrNotFound
	}
	return cloneReadResult(entry), nil
}

func (t *ReadTable) lookupUnique(tenant DomainID, token Token, at time.Time) (ReadResult, error) {
	if t == nil || tenant == "" || token == "" || at.IsZero() {
		return ReadResult{}, ErrNotFound
	}
	var found *ReadResult
	for key, entry := range t.entries {
		if key.tenant != tenant || key.token != token || !entry.visibleAt(at) {
			continue
		}
		if found == nil {
			copy := cloneReadResult(entry)
			found = &copy
			continue
		}
		if found.Target != entry.Target || found.Token != entry.Token || found.Class != entry.Class {
			return ReadResult{}, ErrNotFound
		}
		found.RecordIDs = append(found.RecordIDs, entry.RecordIDs...)
		sort.Slice(found.RecordIDs, func(i, j int) bool { return found.RecordIDs[i] < found.RecordIDs[j] })
		if found.ValidFrom.IsZero() || (!entry.ValidFrom.IsZero() && entry.ValidFrom.Before(found.ValidFrom)) {
			found.ValidFrom = entry.ValidFrom
		}
		if entry.ValidUntil.IsZero() || (!found.ValidUntil.IsZero() && entry.ValidUntil.After(found.ValidUntil)) {
			found.ValidUntil = entry.ValidUntil
		}
	}
	if found == nil {
		return ReadResult{}, ErrNotFound
	}
	return cloneReadResult(*found), nil
}

func (e ReadResult) visibleAt(at time.Time) bool {
	if !e.ValidFrom.IsZero() && at.Before(e.ValidFrom) {
		return false
	}
	if !e.ValidUntil.IsZero() && !at.Before(e.ValidUntil) {
		return false
	}
	return true
}

func cloneReadResult(in ReadResult) ReadResult {
	out := in
	out.RecordIDs = append([]RecordID(nil), in.RecordIDs...)
	return out
}

func (t *ReadTable) len() int {
	if t == nil {
		return 0
	}
	return len(t.entries)
}
