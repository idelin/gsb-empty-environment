package netsec

// Snapshot is an immutable, authorization-scoped view of a Dictionary. It is
// safe to read concurrently and never observes later dictionary mutations.
type Snapshot struct {
	authorized []Tenant
	entries    []Entry
	byWord     map[string][]ID
}

// Entries returns copies of every entry visible in the snapshot, in creation
// order. Mutating the returned entries cannot affect the snapshot.
func (s *Snapshot) Entries() []Entry {
	out := make([]Entry, len(s.entries))
	for i, e := range s.entries {
		out[i] = Entry{
			ID:      e.ID,
			Word:    cloneBytes(e.Word),
			Origins: cloneOrigins(e.Origins),
			Depth:   e.Depth,
			Parents: cloneIDs(e.Parents),
			Revoked: e.Revoked,
		}
	}
	return out
}

// Len returns the number of visible entries.
func (s *Snapshot) Len() int {
	return len(s.entries)
}

// Authorized returns the tenant set the snapshot was scoped to.
func (s *Snapshot) Authorized() []Tenant {
	return cloneTenants(s.authorized)
}

// ByWord returns the IDs of all visible entries whose bytes equal word. Equal
// bytes from different sources stay distinct, so the result may contain more
// than one ID.
func (s *Snapshot) ByWord(word []byte) []ID {
	return cloneIDs(s.byWord[string(word)])
}

// Merge imports the snapshot's visible entries into dst while preserving every
// restriction. Because a snapshot already contains only entries visible inside
// an authorized intersection, merging can never widen access: an entry carrying
// a secret tenant is copied only when the snapshot itself was authorized for
// that tenant.
//
// Imported entries get fresh IDs in dst; the mapping from source ID to new ID
// is returned so callers can retarget parent references. Entries already
// present in dst (matched by full provenance and parent mapping) are reused
// rather than duplicated. The source snapshot remains immutable and reusable.
func Merge(dst *Dictionary, snap *Snapshot) (map[ID]ID, error) {
	if dst == nil {
		return nil, ErrNilDictionary
	}
	if snap == nil {
		return nil, ErrNilSnapshot
	}

	mapping := make(map[ID]ID, snap.Len())

	for _, source := range snap.Entries() {
		var newParents []ID
		for _, pid := range source.Parents {
			mapped, ok := mapping[pid]
			if !ok {
				// A visible parent must have been imported first because the
				// snapshot preserves creation order.
				return nil, ErrUnknownEntry
			}
			newParents = append(newParents, mapped)
		}

		imported, err := dst.importEntry(source.Word, source.Origins, source.Depth, newParents)
		if err != nil {
			return nil, err
		}
		mapping[source.ID] = imported
	}
	return mapping, nil
}
