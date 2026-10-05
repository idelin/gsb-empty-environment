package checkpoint

import "sort"

// DownloadPlan computes the deduplicated, sorted list of chunk hashes
// that must be fetched to materialize target, given the chunks already
// available locally. Multiple references to the same chunk collapse into
// a single download entry.
func (s *Store) DownloadPlan(target string, local []Hash) ([]Hash, error) {
	state, err := s.Expand(target)
	if err != nil {
		return nil, err
	}
	have := make(map[Hash]bool, len(local))
	for _, h := range local {
		have[h] = true
	}
	var plan []Hash
	for _, c := range state {
		if !have[c] {
			have[c] = true // dedup across references
			plan = append(plan, c)
		}
	}
	sort.Slice(plan, func(i, j int) bool { return lessHash(plan[i], plan[j]) })
	return plan, nil
}
