package tokenmigrate

import (
	"fmt"
	"sort"
)

// Materialize applies plan to a copy of in and signs every changed issuance.
// Any error leaves in untouched and returns no partial snapshot.
func (p *Plan) Materialize(in Input, opts Options) (*Snapshot, error) {
	if p == nil {
		return nil, invalid("nil plan")
	}
	if opts.Verifier == nil {
		return nil, ErrNoVerifier
	}
	if p.InputDigest != canonicalInputDigest(in) {
		return nil, ErrInputMismatch
	}
	if p.PlanDigest != canonicalPlanDigest(p.InputDigest, p.At, p.Assignments, p.Reissues, p.TargetScopes) {
		return nil, ErrInputMismatch
	}

	records := append([]Issuance(nil), in.Records...)
	byID := make(map[RecordID]int, len(records))
	for i := range records {
		byID[records[i].ID] = i
	}

	for _, a := range p.Assignments {
		if !a.changed() {
			continue
		}
		index, ok := byID[a.RecordID]
		if !ok {
			return nil, invalid("plan references missing record %s", a.RecordID)
		}
		current := records[index]
		if current.Scope() != a.Origin || current.Token != a.OldToken || current.Commitment != a.Commitment {
			return nil, ErrInputMismatch
		}
		if opts.Issuer == nil {
			return nil, ErrSignerRequired
		}
		next := current
		next.Domain = a.Target.Domain
		next.Generation = a.Target.Generation
		next.Token = a.NewToken
		next.Commitment = a.Class
		next.Signature = nil
		signature, err := opts.Issuer.SignIssuance(next)
		if err != nil {
			return nil, fmt.Errorf("sign migrated record %s: %w", a.RecordID, err)
		}
		if len(signature) == 0 {
			return nil, fmt.Errorf("%w: signer returned an empty signature for %s", ErrSignerRequired, a.RecordID)
		}
		next.Signature = append([]byte(nil), signature...)
		records[index] = next
	}
	sort.Slice(records, func(i, j int) bool { return records[i].ID < records[j].ID })

	shards, err := rebuildShards(records)
	if err != nil {
		return nil, err
	}
	check := &validatedInput{
		in:      in,
		at:      p.At,
		domains: make(map[DomainID]Domain, len(in.Domains)),
		records: make(map[RecordID]Issuance, len(records)),
		classes: newUnionFind(),
	}
	for _, d := range in.Domains {
		check.domains[d.ID] = d
	}
	check.in.Records = records
	if err := validateRecords(check, opts.Verifier); err != nil {
		return nil, err
	}
	check.in.Shards = shards
	if err := validateShards(check); err != nil {
		return nil, err
	}

	return &Snapshot{
		At:      p.At,
		Records: records,
		Shards:  shards,
	}, nil
}

// Migrate plans and atomically materializes a migration. On error it returns
// neither a plan nor a snapshot, and in remains unchanged.
func Migrate(in Input, opts Options) (*Result, error) {
	plan, err := Build(in, opts)
	if err != nil {
		return nil, err
	}
	snapshot, err := plan.Materialize(in, opts)
	if err != nil {
		return nil, err
	}
	return &Result{Plan: plan, Snapshot: *snapshot}, nil
}

func rebuildShards(records []Issuance) ([]Shard, error) {
	type shardKey struct {
		domain DomainID
		gen    Generation
	}
	grouped := make(map[shardKey]*Shard)
	for _, r := range records {
		key := shardKey{domain: r.Domain, gen: r.Generation}
		shard := grouped[key]
		if shard == nil {
			shard = &Shard{
				ID:                 fmt.Sprintf("scope:%s:%d", r.Domain, r.Generation),
				Domain:             r.Domain,
				Generation:         r.Generation,
				TokenToCommitment:  make(map[Token]Commitment),
				CommitmentToTokens: make(map[Commitment][]Token),
			}
			grouped[key] = shard
		}
		if previous, exists := shard.TokenToCommitment[r.Token]; exists && previous != r.Commitment {
			return nil, invalid("token %s maps to two commitments in %s", r.Token, r.Scope())
		}
		shard.TokenToCommitment[r.Token] = r.Commitment
		if !containsToken(shard.CommitmentToTokens[r.Commitment], r.Token) {
			shard.CommitmentToTokens[r.Commitment] = append(shard.CommitmentToTokens[r.Commitment], r.Token)
		}
	}

	shards := make([]Shard, 0, len(grouped))
	for _, shard := range grouped {
		for commitment := range shard.CommitmentToTokens {
			tokens := shard.CommitmentToTokens[commitment]
			sort.Slice(tokens, func(i, j int) bool { return tokens[i] < tokens[j] })
			shard.CommitmentToTokens[commitment] = tokens
		}
		shards = append(shards, *shard)
	}
	sort.Slice(shards, func(i, j int) bool {
		if shards[i].Domain != shards[j].Domain {
			return shards[i].Domain < shards[j].Domain
		}
		if shards[i].Generation != shards[j].Generation {
			return shards[i].Generation < shards[j].Generation
		}
		return shards[i].ID < shards[j].ID
	})
	return shards, nil
}
