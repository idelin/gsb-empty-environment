package tokenmigrate

import (
	"encoding/hex"
	"sort"
	"time"
)

func canonicalInputDigest(in Input) string {
	w := newHashWriter([]byte("tokenmigrate/input/v1"))

	domains := append([]Domain(nil), in.Domains...)
	sort.Slice(domains, func(i, j int) bool { return domains[i].ID < domains[j].ID })
	for _, d := range domains {
		w.str(string(d.ID))
		w.period(d.Valid)
	}

	changes := append([]Change(nil), in.Changes...)
	sort.Slice(changes, func(i, j int) bool { return changes[i].ID < changes[j].ID })
	for _, c := range changes {
		w.str(string(c.ID))
		w.u64(uint64(c.Kind))
		w.period(c.Valid)
		mappings := append([]Mapping(nil), c.Mappings...)
		sort.Slice(mappings, func(i, j int) bool {
			if mappings[i].From != mappings[j].From {
				return mappings[i].From < mappings[j].From
			}
			if mappings[i].To != mappings[j].To {
				return mappings[i].To < mappings[j].To
			}
			return mappings[i].Generation < mappings[j].Generation
		})
		for _, m := range mappings {
			w.str(string(m.From))
			w.str(string(m.To))
			w.u64(uint64(m.Generation))
			if m.Explicit {
				w.str("explicit")
			} else {
				w.str("all")
			}
			for _, id := range sortedStrings(m.Records) {
				w.str(id)
			}
		}
	}

	shards := append([]Shard(nil), in.Shards...)
	sort.Slice(shards, func(i, j int) bool { return shards[i].ID < shards[j].ID })
	for _, s := range shards {
		w.str(s.ID)
		w.str(string(s.Domain))
		w.u64(uint64(s.Generation))

		tokens := make([]Token, 0, len(s.TokenToCommitment))
		for token := range s.TokenToCommitment {
			tokens = append(tokens, token)
		}
		sort.Slice(tokens, func(i, j int) bool { return tokens[i] < tokens[j] })
		for _, token := range tokens {
			w.str(string(token))
			w.str(string(s.TokenToCommitment[token]))
		}

		commitments := make([]Commitment, 0, len(s.CommitmentToTokens))
		for commitment := range s.CommitmentToTokens {
			commitments = append(commitments, commitment)
		}
		sort.Slice(commitments, func(i, j int) bool { return commitments[i] < commitments[j] })
		for _, commitment := range commitments {
			w.str(string(commitment))
			for _, token := range sortedStrings(s.CommitmentToTokens[commitment]) {
				w.str(token)
			}
		}
	}

	records := append([]Issuance(nil), in.Records...)
	sort.Slice(records, func(i, j int) bool { return records[i].ID < records[j].ID })
	for _, r := range records {
		w.str(string(r.ID))
		w.str(string(r.Domain))
		w.u64(uint64(r.Generation))
		w.str(string(r.Token))
		w.str(string(r.Commitment))
		w.period(r.Valid)
		w.bytes(r.Signature)
	}

	proofs := append([]EqualityProof(nil), in.Proofs...)
	sort.Slice(proofs, func(i, j int) bool { return proofs[i].ID < proofs[j].ID })
	for _, p := range proofs {
		w.str(p.ID)
		left, right := p.Left, p.Right
		if right < left {
			left, right = right, left
		}
		w.str(string(left))
		w.str(string(right))
		sigs := append([]EqualitySignature(nil), p.Signatures...)
		sort.Slice(sigs, func(i, j int) bool { return sigs[i].Signer < sigs[j].Signer })
		for _, sig := range sigs {
			w.str(string(sig.Signer))
			w.bytes(sig.Signature)
		}
	}

	return hex.EncodeToString(w.sum())
}

func canonicalPlanDigest(inputDigest string, at time.Time, assignments []Assignment, reissues []Reissue, targets []Scope) string {
	w := newHashWriter([]byte("tokenmigrate/plan/v1"), []byte(inputDigest))
	w.timestamp(at)

	as := append([]Assignment(nil), assignments...)
	sort.Slice(as, func(i, j int) bool { return as[i].RecordID < as[j].RecordID })
	for _, a := range as {
		w.str(string(a.RecordID))
		w.str(a.Origin.String())
		w.str(a.Target.String())
		w.str(string(a.OldToken))
		w.str(string(a.NewToken))
		w.str(string(a.Commitment))
		w.str(string(a.Class))
		if a.Moved {
			w.str("moved")
		}
		if a.Reissued {
			w.str("reissued")
		}
		if a.Canonical {
			w.str("canonical")
		}
	}

	rs := append([]Reissue(nil), reissues...)
	sort.Slice(rs, func(i, j int) bool { return rs[i].RecordID < rs[j].RecordID })
	for _, r := range rs {
		w.str(string(r.RecordID))
		w.str(r.Origin.String())
		w.str(string(r.OldToken))
		w.str(r.Target.String())
		w.str(string(r.NewToken))
		w.str(string(r.Class))
	}

	for _, target := range targets {
		w.str(target.String())
	}
	return hex.EncodeToString(w.sum())
}
