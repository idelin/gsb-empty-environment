package tokenmigrate

import (
	"fmt"
	"sort"
	"strconv"
	"time"
)

type unionFind struct {
	parent map[Commitment]Commitment
}

func newUnionFind() *unionFind {
	return &unionFind{parent: make(map[Commitment]Commitment)}
}

func (u *unionFind) add(c Commitment) {
	if _, ok := u.parent[c]; !ok {
		u.parent[c] = c
	}
}

func (u *unionFind) find(c Commitment) Commitment {
	u.add(c)
	root := c
	for u.parent[root] != root {
		root = u.parent[root]
	}
	for u.parent[c] != c {
		next := u.parent[c]
		u.parent[c] = root
		c = next
	}
	return root
}

func (u *unionFind) union(a, b Commitment) {
	ra, rb := u.find(a), u.find(b)
	if ra == rb {
		return
	}
	// The lexicographically smaller commitment is always the class
	// representative. This makes certificates deterministic.
	if rb < ra {
		ra, rb = rb, ra
	}
	u.parent[rb] = ra
}

func (u *unionFind) members() map[Commitment][]Commitment {
	out := make(map[Commitment][]Commitment)
	all := make([]Commitment, 0, len(u.parent))
	for c := range u.parent {
		all = append(all, c)
	}
	sort.Slice(all, func(i, j int) bool { return all[i] < all[j] })
	for _, c := range all {
		root := u.find(c)
		out[root] = append(out[root], c)
	}
	return out
}

type validatedInput struct {
	in          Input
	at          time.Time
	domains     map[DomainID]Domain
	records     map[RecordID]Issuance
	classes     *unionFind
	inputDigest string
}

func validateInput(in Input, at time.Time, verifier Verifier) (*validatedInput, error) {
	if verifier == nil {
		return nil, ErrNoVerifier
	}
	v := &validatedInput{
		in:      in,
		at:      at,
		domains: make(map[DomainID]Domain, len(in.Domains)),
		records: make(map[RecordID]Issuance, len(in.Records)),
		classes: newUnionFind(),
	}

	for _, d := range in.Domains {
		if d.ID == "" {
			return nil, invalid("domain with empty ID")
		}
		if _, exists := v.domains[d.ID]; exists {
			return nil, invalid("duplicate domain %s", d.ID)
		}
		if !d.Valid.Valid() {
			return nil, invalid("domain %s has an invalid validity period", d.ID)
		}
		v.domains[d.ID] = d
	}
	if len(v.domains) == 0 {
		return nil, invalid("domain set is empty")
	}

	if err := validateRecords(v, verifier); err != nil {
		return nil, err
	}
	if err := validateChanges(v); err != nil {
		return nil, err
	}
	if err := validateShards(v); err != nil {
		return nil, err
	}
	if err := validateProofs(v, verifier); err != nil {
		return nil, err
	}
	v.inputDigest = canonicalInputDigest(in)
	return v, nil
}

func validateChanges(v *validatedInput) error {
	seen := make(map[ChangeID]struct{}, len(v.in.Changes))
	for _, c := range v.in.Changes {
		if c.ID == "" {
			return invalid("change with empty ID")
		}
		if _, exists := seen[c.ID]; exists {
			return invalid("duplicate change %s", c.ID)
		}
		seen[c.ID] = struct{}{}
		if c.Kind != MergeChange && c.Kind != SplitChange {
			return invalid("change %s has unknown kind", c.ID)
		}
		if !c.Valid.Valid() {
			return invalid("change %s has an invalid validity period", c.ID)
		}
		if len(c.Mappings) == 0 {
			return invalid("change %s has no mappings", c.ID)
		}

		froms := make(map[DomainID]struct{})
		tos := make(map[DomainID]struct{})
		explicitSeen := make(map[RecordID]DomainID)
		for _, m := range c.Mappings {
			if m.From == "" || m.To == "" {
				return invalid("change %s has an empty mapping endpoint", c.ID)
			}
			if _, ok := v.domains[m.From]; !ok {
				return invalid("change %s references unknown source domain %s", c.ID, m.From)
			}
			if _, ok := v.domains[m.To]; !ok {
				return invalid("change %s references unknown target domain %s", c.ID, m.To)
			}
			froms[m.From] = struct{}{}
			tos[m.To] = struct{}{}
			if m.Explicit {
				for _, id := range m.Records {
					if id == "" {
						return invalid("change %s contains an empty record ID", c.ID)
					}
					if previous, exists := explicitSeen[id]; exists {
						return invalid("record %s is authorized more than once in change %s (targets %s and %s)", id, c.ID, previous, m.To)
					}
					explicitSeen[id] = m.To
					record, ok := v.records[id]
					if !ok {
						return invalid("change %s authorizes unknown record %s", c.ID, id)
					}
					if record.Domain != m.From {
						return invalid("change %s authorizes record %s from %s, but mapping source is %s", c.ID, id, record.Domain, m.From)
					}
				}
			}
		}

		switch c.Kind {
		case MergeChange:
			if len(tos) != 1 {
				return invalid("merge change %s must have exactly one target", c.ID)
			}
			if len(froms) != len(c.Mappings) {
				return invalid("merge change %s repeats a source domain", c.ID)
			}
		case SplitChange:
			if len(froms) != 1 {
				return invalid("split change %s must have exactly one source", c.ID)
			}
			if len(tos) != len(c.Mappings) {
				return invalid("split change %s repeats a target domain", c.ID)
			}
			for _, m := range c.Mappings {
				if !m.Explicit {
					return invalid("split change %s target %s must use an explicit record subset", c.ID, m.To)
				}
			}
		}
	}

	activeSources := make(map[DomainID]ChangeID)
	for _, c := range v.in.Changes {
		if !c.Valid.Contains(v.at) {
			continue
		}
		for _, m := range c.Mappings {
			if !v.domains[m.From].Valid.Contains(v.at) {
				return invalid("active change %s has inactive source domain %s", c.ID, m.From)
			}
			if !v.domains[m.To].Valid.Contains(v.at) {
				return invalid("active change %s has inactive target domain %s", c.ID, m.To)
			}
			if previous, exists := activeSources[m.From]; exists && previous != c.ID {
				return invalid("source domain %s is active in both %s and %s", m.From, previous, c.ID)
			}
			activeSources[m.From] = c.ID
		}
	}
	return nil
}

func validateRecords(v *validatedInput, verifier Verifier) error {
	scopeTokens := make(map[string]Commitment)
	for _, r := range v.in.Records {
		if r.ID == "" {
			return invalid("record with empty ID")
		}
		if _, exists := v.records[r.ID]; exists {
			return invalid("duplicate issuance record %s", r.ID)
		}
		if r.Domain == "" || r.Token == "" || r.Commitment == "" {
			return invalid("record %s has an empty domain, token, or commitment", r.ID)
		}
		if _, ok := v.domains[r.Domain]; !ok {
			return invalid("record %s references unknown domain %s", r.ID, r.Domain)
		}
		if !r.Valid.Valid() {
			return invalid("record %s has an invalid validity period", r.ID)
		}
		key := scopeTokenKey(r.Scope(), r.Token)
		if previous, exists := scopeTokens[key]; exists && previous != r.Commitment {
			return invalid("token %s is bound to two commitments in scope %s", r.Token, r.Scope())
		}
		scopeTokens[key] = r.Commitment
		if err := verifier.VerifyIssuance(r); err != nil {
			return invalid("record %s: %v", r.ID, err)
		}
		v.records[r.ID] = r
		v.classes.add(r.Commitment)
	}
	return nil
}

func validateShards(v *validatedInput) error {
	shardIDs := make(map[string]struct{}, len(v.in.Shards))
	tokenBindings := make(map[string]Commitment)
	for _, s := range v.in.Shards {
		if s.ID == "" {
			return invalid("shard with empty ID")
		}
		if _, exists := shardIDs[s.ID]; exists {
			return invalid("duplicate shard %s", s.ID)
		}
		shardIDs[s.ID] = struct{}{}
		if _, ok := v.domains[s.Domain]; !ok {
			return invalid("shard %s references unknown domain %s", s.ID, s.Domain)
		}
		if s.TokenToCommitment == nil {
			return invalid("shard %s has no forward map", s.ID)
		}
		if s.CommitmentToTokens == nil {
			return invalid("shard %s has no reverse map", s.ID)
		}

		for token, commitment := range s.TokenToCommitment {
			if token == "" || commitment == "" {
				return invalid("shard %s contains an empty token or commitment", s.ID)
			}
			if !containsToken(s.CommitmentToTokens[commitment], token) {
				return invalid("shard %s forward map is not reflected in reverse map", s.ID)
			}
			key := scopeTokenKey(s.Scope(), token)
			if previous, exists := tokenBindings[key]; exists && previous != commitment {
				return invalid("token %s binds to different commitments in scope %s", token, s.Scope())
			}
			tokenBindings[key] = commitment
		}
		for commitment, tokens := range s.CommitmentToTokens {
			if commitment == "" {
				return invalid("shard %s contains an empty commitment", s.ID)
			}
			if len(tokens) == 0 {
				return invalid("shard %s has an empty reverse alias list", s.ID)
			}
			for _, token := range tokens {
				if got, ok := s.TokenToCommitment[token]; !ok || got != commitment {
					return invalid("shard %s reverse map is not reflected in forward map", s.ID)
				}
			}
		}
	}

	for _, r := range v.in.Records {
		key := scopeTokenKey(r.Scope(), r.Token)
		commitment, ok := tokenBindings[key]
		if !ok {
			return invalid("record %s has no shard binding for token %s in %s", r.ID, r.Token, r.Scope())
		}
		if commitment != r.Commitment {
			return invalid("record %s disagrees with shard binding for token %s", r.ID, r.Token)
		}
	}
	return nil
}

func validateProofs(v *validatedInput, verifier Verifier) error {
	seen := make(map[string]struct{}, len(v.in.Proofs))
	for _, p := range v.in.Proofs {
		if p.ID == "" {
			return invalid("equality proof with empty ID")
		}
		if _, exists := seen[p.ID]; exists {
			return invalid("duplicate equality proof %s", p.ID)
		}
		seen[p.ID] = struct{}{}
		if p.Left == "" || p.Right == "" {
			return invalid("equality proof %s has an empty commitment", p.ID)
		}
		if err := verifier.VerifyEquality(p); err != nil {
			return invalid("equality proof %s: %v", p.ID, err)
		}
		v.classes.add(p.Left)
		v.classes.add(p.Right)
		v.classes.union(p.Left, p.Right)
	}
	return nil
}

func scopeTokenKey(scope Scope, token Token) string {
	return keyPart(string(scope.Domain)) +
		keyPart(strconv.FormatUint(uint64(scope.Generation), 10)) +
		keyPart(string(token))
}

func keyPart(value string) string {
	return strconv.Itoa(len(value)) + ":" + value
}

func containsToken(tokens []Token, wanted Token) bool {
	for _, token := range tokens {
		if token == wanted {
			return true
		}
	}
	return false
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidInput, fmt.Sprintf(format, args...))
}
