package netsec

import "sort"

// SearchRequest describes the host-provided memory, attacker capability, and
// success predicate for one unlink version.
type SearchRequest struct {
	Memory Memory
	Header uint32

	// PrevAllowed and NextAllowed are the only values the attacker may place
	// in the two fields of Header.
	PrevAllowed ValueSet
	NextAllowed ValueSet

	// Protected is the set of words that must not acquire AttackValue. A
	// value written only to a non-protected word is not a successful attack.
	Protected   WordSet
	AttackValue uint32

	// FreeHeaders is the host's registry of free block headers. VersionC
	// requires both neighbours to belong to this set.
	FreeHeaders HeaderSet

	Version Version
}

// SearchResult reports the lexicographically first successful attack.
//
// Winner contains the complete replay, including every read and write in
// Trace. Attempts counts the in-range candidate pairs that were replayed;
// candidates whose prev/next addresses cannot be legal are skipped because
// every version rejects them during the precheck.
type SearchResult struct {
	Found           bool
	Version         Version
	Prev            uint32
	Next            uint32
	Attempts        int
	AttackAddresses []uint32
	Winner          ReplayResult
	Trace           []Access
	Reason          RejectReason
}

func sortedUnique(values []uint32) []uint32 {
	out := append([]uint32(nil), values...)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	if len(out) < 2 {
		return out
	}
	write := 1
	for read := 1; read < len(out); read++ {
		if out[read] != out[write-1] {
			out[write] = out[read]
			write++
		}
	}
	return out[:write]
}

func attackedWords(r ReplayResult, protected WordSet, attackValue uint32) []uint32 {
	if !r.Accepted {
		return nil
	}
	seen := make(map[uint32]bool)
	var hits []uint32
	for _, addr := range protected {
		if addr >= WordCount || seen[addr] {
			continue
		}
		seen[addr] = true
		// Before already includes the attacker's permitted header edits.
		// Requiring a change relative to Before ensures that the attack is
		// caused by the unlink writes, not by the attacker's own setup.
		if r.Before[addr] != attackValue && r.After[addr] == attackValue {
			hits = append(hits, addr)
		}
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i] < hits[j] })
	return hits
}

// Search enumerates the explicitly allowed (prev, next) pairs in ascending
// numeric order and returns the first one whose unlink is accepted and changes
// a protected word to AttackValue.
//
// Ordinary chain corruption is not sufficient: the protected word must end
// with exactly AttackValue. Rejected candidates never write, so they cannot
// satisfy the predicate.
func Search(req SearchRequest) SearchResult {
	result := SearchResult{Version: req.Version}
	if !req.Version.Valid() {
		result.Reason = RejectInvalidVersion
		return result
	}
	if !headerValid(req.Header) {
		result.Reason = RejectInvalidHeader
		return result
	}

	prevs := sortedUnique(req.PrevAllowed)
	nexts := sortedUnique(req.NextAllowed)

	for _, prev := range prevs {
		// No version can accept a pair that would write outside memory.
		if uint64(prev)+1 >= WordCount {
			continue
		}
		for _, next := range nexts {
			if uint64(next) >= WordCount {
				continue
			}
			result.Attempts++
			attempt := Replay(req.Memory, req.Header, prev, next, req.Version, req.FreeHeaders)
			if !attempt.Accepted {
				continue
			}
			hits := attackedWords(attempt, req.Protected, req.AttackValue)
			if len(hits) == 0 {
				continue
			}

			result.Found = true
			result.Prev = prev
			result.Next = next
			result.AttackAddresses = hits
			result.Winner = attempt
			result.Trace = attempt.Trace
			return result
		}
	}

	return result
}
