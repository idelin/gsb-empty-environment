package credentialauth

// matchGraph is the bipartite graph used for global consumption.  Left
// vertices are transaction slots and right vertices are logical credentials.
// Each credential has capacity one across every batch.
type matchGraph struct {
	adjacency   [][]int
	credentials int
}

func (g *matchGraph) credentialCount() int {
	return g.credentials
}

// maximumMatching returns a deterministic maximum matching.  bannedCredential
// and bannedSlot may be -1.  A banned slot is not required to be matched; a
// banned credential cannot be used.  The returned slice maps slot index to
// credential index (-1 when unmatched).
func (g *matchGraph) maximumMatching(bannedCredential, bannedSlot int) ([]int, bool) {
	slotMatch := make([]int, len(g.adjacency))
	for i := range slotMatch {
		slotMatch[i] = -1
	}
	credentialMatch := make([]int, g.credentials)
	for i := range credentialMatch {
		credentialMatch[i] = -1
	}

	var augment func(slot int, seen []bool) bool
	augment = func(slot int, seen []bool) bool {
		for _, credential := range g.adjacency[slot] {
			if credential == bannedCredential || credential < 0 || credential >= g.credentials || seen[credential] {
				continue
			}
			seen[credential] = true
			previous := credentialMatch[credential]
			if previous == -1 || augment(previous, seen) {
				credentialMatch[credential] = slot
				slotMatch[slot] = credential
				return true
			}
		}
		return false
	}

	for slot := range g.adjacency {
		if slot == bannedSlot {
			continue
		}
		seen := make([]bool, g.credentials)
		if !augment(slot, seen) {
			return slotMatch, false
		}
	}
	return slotMatch, true
}
