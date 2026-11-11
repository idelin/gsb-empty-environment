package netsec

import "slices"

// Tenant identifies a fuzzing tenant on the shared platform.
type Tenant string

// Origin binds a token to the concrete extraction event that produced it.
//
// Tenant is the tenant whose input supplied the bytes. Input is the digest of
// that input. Public reports whether the tenant attests that the bytes are
// publicly provable (a documented protocol constant, public specification and
// similar) rather than tenant-specific material. Origins with equal Tenant and
// Input but different Public values are distinct origins.
type Origin struct {
	Tenant Tenant
	Input  Digest
	Public bool
}

// ID is the identity of an entry within a Dictionary. IDs are assigned
// monotonically and are never reused, even after revocation.
type ID uint64

// Entry is an immutable, externally visible dictionary token together with the
// complete set of origins it depends on. Equal bytes from different sources
// produce distinct entries, so callers must reason about Origins rather than
// Word alone.
type Entry struct {
	// ID is the unique identity of the entry.
	ID ID

	// Word is the token bytes offered to the mutation host.
	Word []byte

	// Origins is the full provenance closure, including every origin of every
	// transitive parent. It is sorted and deduplicated.
	Origins []Origin

	// Depth is 0 for directly extracted entries and increases by one for every
	// derivation level, computed as one plus the maximum parent depth.
	Depth int

	// Parents are the immediate entries this entry was derived from; empty for
	// directly extracted entries.
	Parents []ID

	// Revoked reports whether the entry is dead because one of its origins was
	// revoked.
	Revoked bool
}

// SecretTenants returns the sorted, deduplicated set of tenants that supplied
// non-public origins. Access to the entry requires all of these tenants to be
// authorized.
func (e Entry) SecretTenants() []Tenant {
	seen := map[Tenant]struct{}{}
	var out []Tenant
	for _, origin := range e.Origins {
		if origin.Public {
			continue
		}
		if _, ok := seen[origin.Tenant]; ok {
			continue
		}
		seen[origin.Tenant] = struct{}{}
		out = append(out, origin.Tenant)
	}
	slices.Sort(out)
	return out
}

// IsPublic reports whether every origin attests public provability. Such an
// entry carries no secret tenant restriction but remains bound to its origins.
func (e Entry) IsPublic() bool {
	return len(e.SecretTenants()) == 0
}

// sortOrigins orders origins deterministically and removes duplicates. Two
// origins are equal only when Tenant, Input and Public all agree, so a public
// attestation can never collapse onto a secret one.
func sortOrigins(origins []Origin) []Origin {
	if len(origins) == 0 {
		return nil
	}
	unique := make([]Origin, 0, len(origins))
	seen := map[Origin]struct{}{}
	for _, origin := range origins {
		if _, ok := seen[origin]; ok {
			continue
		}
		seen[origin] = struct{}{}
		unique = append(unique, origin)
	}
	slices.SortFunc(unique, func(a, b Origin) int {
		if a.Tenant != b.Tenant {
			if a.Tenant < b.Tenant {
				return -1
			}
			return 1
		}
		if cmp := a.Input.Compare(b.Input); cmp != 0 {
			return cmp
		}
		switch {
		case a.Public == b.Public:
			return 0
		case !a.Public:
			return -1
		default:
			return 1
		}
	})
	return unique
}

func cloneBytes(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out
}

func cloneOrigins(origins []Origin) []Origin {
	if len(origins) == 0 {
		return nil
	}
	out := make([]Origin, len(origins))
	copy(out, origins)
	return out
}

func cloneIDs(ids []ID) []ID {
	if len(ids) == 0 {
		return nil
	}
	out := make([]ID, len(ids))
	copy(out, ids)
	return out
}

func cloneTenants(tenants []Tenant) []Tenant {
	if len(tenants) == 0 {
		return nil
	}
	out := make([]Tenant, len(tenants))
	copy(out, tenants)
	return out
}
