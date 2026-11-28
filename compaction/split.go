package compaction

import "sort"

// Subcompaction is one disjoint half-open key span [Lo, Hi) of a compaction,
// together with the range tombstones clipped to exactly that span.
type Subcompaction struct {
	Index      int
	Lo         UserKey // inclusive; empty means -infinity
	Hi         UserKey // exclusive; empty means +infinity
	Tombstones []RangeTombstone
	Bytes      int64
}

// InSpan reports whether key k belongs to the half-open span [Lo, Hi).
func (s *Subcompaction) InSpan(k UserKey) bool {
	if s.Lo != "" && k < s.Lo {
		return false
	}
	if s.Hi != "" && k >= s.Hi {
		return false
	}
	return true
}

// NeedsSubcompaction reports whether a job's input volume exceeds the
// configured subcompaction threshold so a split is worthwhile.
func (e *Engine) NeedsSubcompaction(job *Job) bool {
	return job.TotalBytes > e.cfg.SubcompactionBytes && e.cfg.MaxSubcompactions > 1
}

// PlanSubcompactions derives at most MaxSubcompactions disjoint half-open key
// spans from the smallest/largest boundary keys of the input files and the
// output-level files, then clips every input range tombstone to each span it
// intersects. A single span (no split) is produced below the byte threshold.
// It takes no engine lock and reads only the supplied immutable version.
func (e *Engine) PlanSubcompactions(job *Job) []*Subcompaction {
	return e.planSubcompactions(job, e.CurrentVersion())
}

func (e *Engine) planSubcompactions(job *Job, version *Version) []*Subcompaction {
	maxN := e.cfg.MaxSubcompactions
	if maxN < 1 {
		maxN = 1
	}
	if !e.NeedsSubcompaction(job) {
		return e.buildSubcompactions(job, nil)
	}

	// Candidate split boundaries are the start keys of every input and every
	// output-level SST, plus the smallest key of every entry inside the input
	// files (a single large SST still needs internal cut points). Cutting on
	// real keys keeps each output SST aligned with existing level runs.
	candidateSet := map[UserKey]struct{}{}
	for _, f := range job.Inputs {
		candidateSet[f.Smallest] = struct{}{}
		for _, en := range f.Entries {
			candidateSet[en.UserKey] = struct{}{}
		}
	}
	for _, f := range version.Files(job.OutputLevel) {
		if f.Overlaps(job.Range) {
			candidateSet[f.Smallest] = struct{}{}
		}
	}
	candidates := make([]UserKey, 0, len(candidateSet))
	for k := range candidateSet {
		if k > job.Range.Smallest {
			candidates = append(candidates, k)
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i] < candidates[j] })

	n := maxN
	if len(candidates)+1 < n {
		n = len(candidates) + 1
	}
	if n < 1 {
		n = 1
	}

	// Byte position of each candidate boundary: total bytes of input entries
	// whose user key is strictly below the boundary. File sizes alone cannot
	// subdivide a single large SST, so positions are derived from entries.
	keyBytes := map[UserKey]int64{}
	var order []UserKey
	seenKey := map[UserKey]struct{}{}
	for _, f := range job.Inputs {
		for _, en := range f.Entries {
			if _, ok := seenKey[en.UserKey]; !ok {
				seenKey[en.UserKey] = struct{}{}
				order = append(order, en.UserKey)
			}
			keyBytes[en.UserKey] += entrySize(en)
		}
	}
	sort.Slice(order, func(i, j int) bool { return order[i] < order[j] })
	position := map[UserKey]int64{}
	var running int64
	for _, k := range order {
		position[k] = running
		running += keyBytes[k]
	}
	positionAt := func(boundary UserKey) int64 {
		// Bytes of entries with key < boundary: find the running prefix.
		idx := sort.Search(len(order), func(i int) bool { return order[i] >= boundary })
		if idx == 0 {
			return 0
		}
		return position[order[idx-1]] + keyBytes[order[idx-1]]
	}

	// Byte-balanced cuts: for each of the n-1 cut points pick the candidate
	// boundary whose byte position is closest to the ideal k*total/n mark.
	target := job.TotalBytes / int64(n)
	if target <= 0 {
		target = 1
	}
	cuts := make([]UserKey, 0, n-1)
	for k := 1; k < n; k++ {
		ideal := target * int64(k)
		var best UserKey
		var bestDelta int64 = -1
		for _, b := range candidates {
			if len(cuts) > 0 && b <= cuts[len(cuts)-1] {
				continue
			}
			pos := positionAt(b)
			delta := pos - ideal
			if delta < 0 {
				delta = -delta
			}
			if bestDelta < 0 || delta < bestDelta {
				bestDelta = delta
				best = b
			}
		}
		if best != "" {
			cuts = append(cuts, best)
		}
	}

	// Top up if the closest-boundary search under-filled (rare with sparse
	// candidates) so the cap is honored without empty spans.
	if len(cuts) < n-1 {
		used := map[UserKey]struct{}{}
		for _, c := range cuts {
			used[c] = struct{}{}
		}
		for i := len(candidates) - 1; i >= 0 && len(cuts) < n-1; i-- {
			if _, ok := used[candidates[i]]; !ok {
				cuts = append(cuts, candidates[i])
				used[candidates[i]] = struct{}{}
			}
		}
		sort.Slice(cuts, func(i, j int) bool { return cuts[i] < cuts[j] })
	}

	return e.buildSubcompactions(job, cuts)
}

// buildSubcompactions turns cut boundaries into spans spanning the whole job
// range and clips every input range tombstone onto every intersecting span.
func (e *Engine) buildSubcompactions(job *Job, cuts []UserKey) []*Subcompaction {
	bounds := append([]UserKey{job.Range.Smallest}, cuts...)
	bounds = append(bounds, "")

	subs := make([]*Subcompaction, 0, len(bounds)-1)
	for i := 0; i < len(bounds)-1; i++ {
		lo := bounds[i]
		hi := bounds[i+1]
		sub := &Subcompaction{Index: i, Lo: lo, Hi: hi}
		for _, f := range job.Inputs {
			for _, t := range f.RangeDeletes {
				if clipped, ok := clipTombstone(t, lo, hi); ok {
					sub.Tombstones = append(sub.Tombstones, clipped)
				}
			}
		}
		sort.SliceStable(sub.Tombstones, func(a, b int) bool {
			if sub.Tombstones[a].Start != sub.Tombstones[b].Start {
				return sub.Tombstones[a].Start < sub.Tombstones[b].Start
			}
			return sub.Tombstones[a].Seq > sub.Tombstones[b].Seq
		})
		for _, f := range job.Inputs {
			if f.Overlaps(KeyRange{Smallest: orMin(lo), Largest: orMax(hi)}) {
				sub.Bytes += f.SizeBytes
			}
		}
		subs = append(subs, sub)
	}
	return subs
}

func orMin(k UserKey) UserKey {
	if k == "" {
		return UserKey("\x00")
	}
	return k
}

func orMax(k UserKey) UserKey {
	if k == "" {
		return UserKey("\xff\xff\xff\xff")
	}
	return prevKey(k)
}
