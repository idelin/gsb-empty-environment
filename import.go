package netsec

import "slices"

// importEntry reconstructs an entry inside the receiver dictionary. It is the
// only path, together with Add and Derive, that creates entries, and therefore
// centralizes the revoked-source check: a snapshot cannot smuggle in anything
// whose provenance contains a revoked source of this dictionary.
func (d *Dictionary) importEntry(word []byte, origins []Origin, depth int, parents []ID) (ID, error) {
	if len(word) == 0 {
		return 0, ErrEmptyWord
	}
	if len(origins) == 0 {
		return 0, ErrZeroDigest
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	for _, origin := range origins {
		if err := validateOrigin(origin); err != nil {
			return 0, err
		}
		if _, ok := d.revoked[revokeKey{tenant: origin.Tenant, input: origin.Input}]; ok {
			return 0, ErrRevoked
		}
	}

	// Reuse an existing entry with identical provenance and remapped parents so
	// repeated merges are idempotent. Identity is provenance, not byte content.
	for _, id := range d.order {
		existing := d.entries[id]
		if existing.dead || string(existing.word) != string(word) || existing.depth != depth {
			continue
		}
		if !sameOrigins(existing.origins, origins) || !sameIDs(existing.parents, parents) {
			continue
		}
		return id, nil
	}

	id := d.nextIDLocked()
	d.entries[id] = &entry{
		id:      id,
		word:    cloneBytes(word),
		origins: sortOrigins(cloneOrigins(origins)),
		depth:   depth,
		parents: cloneIDs(parents),
	}
	d.order = append(d.order, id)
	return id, nil
}

func sameOrigins(a, b []Origin) bool {
	return slices.Equal(a, b)
}

func sameIDs(a, b []ID) bool {
	return slices.Equal(a, b)
}
