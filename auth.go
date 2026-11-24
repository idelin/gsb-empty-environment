package readproof

import (
	"fmt"
	"sort"
)

type activeSet struct {
	byID    map[string]Credential
	ordered []Credential
	// scoped contains every credential scoped to this tenant/root, including
	// credentials removed by revocation.  It is used only to explain gaps.
	scoped []Credential
}

func buildActiveSet(view AuthorizationView, root Digest, tenant string) (*activeSet, error) {
	revokedThrough := make(map[string]uint64)
	revokedAll := make(map[string]bool)
	for _, rev := range view.Revocations {
		if rev.CredentialID == "" {
			return nil, fmt.Errorf("%w: empty credential ID in revocation", ErrUnauthorized)
		}
		if rev.AllGenerations {
			revokedAll[rev.CredentialID] = true
		}
		if rev.Generation > revokedThrough[rev.CredentialID] {
			revokedThrough[rev.CredentialID] = rev.Generation
		}
	}

	best := make(map[string]Credential)
	scoped := make([]Credential, 0, len(view.Credentials))
	for _, cred := range view.Credentials {
		if cred.ID == "" {
			return nil, fmt.Errorf("%w: empty credential ID", ErrUnauthorized)
		}
		if !cred.Range.Valid() {
			return nil, fmt.Errorf("%w: credential %s", ErrInvalidRange, cred.ID)
		}
		if cred.Tenant != tenant || cred.Root != root {
			continue
		}
		scoped = append(scoped, cred)
		prev, exists := best[cred.ID]
		if !exists {
			best[cred.ID] = cred
			continue
		}
		switch {
		case cred.Generation > prev.Generation:
			best[cred.ID] = cred
		case cred.Generation == prev.Generation && cred != prev:
			return nil, fmt.Errorf("%w: conflicting credentials with ID %q and generation %d",
				ErrUnauthorized, cred.ID, cred.Generation)
		}
	}

	active := &activeSet{
		byID:    make(map[string]Credential, len(best)),
		ordered: make([]Credential, 0, len(best)),
		scoped:  scoped,
	}
	for id, cred := range best {
		if revokedAll[id] || cred.Generation <= revokedThrough[id] {
			continue
		}
		active.byID[id] = cred
		active.ordered = append(active.ordered, cred)
	}
	sort.Slice(active.ordered, func(i, j int) bool {
		if active.ordered[i].ID != active.ordered[j].ID {
			return active.ordered[i].ID < active.ordered[j].ID
		}
		return active.ordered[i].Generation < active.ordered[j].Generation
	})
	return active, nil
}

func (a *activeSet) anyScopedAt(pos uint64) bool {
	for _, cred := range a.scoped {
		if cred.Range.Start <= pos && pos < cred.Range.End {
			return true
		}
	}
	return false
}

func (a *activeSet) get(id string) (Credential, bool) {
	cred, ok := a.byID[id]
	return cred, ok
}
