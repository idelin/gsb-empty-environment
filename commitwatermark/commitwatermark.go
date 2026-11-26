package commitwatermark

import (
	"errors"
	"fmt"
	"sort"
)

// ErrInvalidInput is returned when the caller-supplied log or configuration
// cannot describe a legal Raft state.
var ErrInvalidInput = errors.New("commitwatermark: invalid input")

type quorumGroup struct {
	name   string
	config Config
	voters []NodeID
}
type segment struct {
	from   uint64
	groups []quorumGroup
}

// Analyze returns the largest commitIndex the leader described by in may
// safely advance to, together with the quorum groups blocking full
// advancement.
func Analyze(in Input) (Result, error) {
	if err := validate(in); err != nil {
		return Result{}, err
	}
	segments := buildSegments(in)
	effective := effectiveMatches(in)
	// The committable anchor must be an entry from the leader's own term.
	target := uint64(0)
	for _, entry := range in.Entries {
		if entry.Term == in.CurrentTerm {
			target = entry.Index
		}
	}
	result := Result{CommitIndex: in.PrevCommit, TargetIndex: target}
	if target == 0 || target <= in.PrevCommit {
		return result, nil
	}
	// Earlier current-term indices are tested first so that the maximum safe
	// watermark is found precisely instead of only testing the log tail.
	for _, entry := range in.Entries {
		k := entry.Index
		if k <= in.PrevCommit || k > target {
			continue
		}
		if entry.Term != in.CurrentTerm {
			continue
		}
		if committedAt(k, in.PrevCommit, segments, effective) {
			result.CommitIndex = k
		}
	}
	result.Bottlenecks = collectBottlenecks(target, in.PrevCommit, segments, effective)
	return result, nil
}
func validate(in Input) error {
	if in.CurrentTerm == 0 {
		return fmt.Errorf("%w: CurrentTerm must be positive", ErrInvalidInput)
	}
	if len(in.Initial.Voters) == 0 {
		return fmt.Errorf("%w: initial configuration has no voters", ErrInvalidInput)
	}
	if duplicateID(in.Initial.Voters) {
		return fmt.Errorf("%w: duplicate initial voter", ErrInvalidInput)
	}
	if len(in.Entries) == 0 {
		return nil
	}
	base := in.Entries[0].Index
	prevTerm := uint64(0)
	for i, entry := range in.Entries {
		if entry.Index != base+uint64(i) {
			return fmt.Errorf("%w: entries must be contiguous", ErrInvalidInput)
		}
		if entry.Term == 0 || entry.Term > in.CurrentTerm {
			return fmt.Errorf("%w: entry term outside (0, CurrentTerm]", ErrInvalidInput)
		}
		if entry.Term < prevTerm {
			return fmt.Errorf("%w: entry terms must be non-decreasing", ErrInvalidInput)
		}
		prevTerm = entry.Term
		if entry.Change != nil {
			if err := validateChange(entry.Change); err != nil {
				return err
			}
		}
	}
	lastIndex := in.Entries[len(in.Entries)-1].Index
	if in.PrevCommit > lastIndex {
		return fmt.Errorf("%w: PrevCommit beyond log", ErrInvalidInput)
	}
	return nil
}
func validateChange(change *ConfigChange) error {
	if change.Kind != ChangeEnterJoint && change.Kind != ChangeLeaveJoint {
		return fmt.Errorf("%w: unknown config change kind", ErrInvalidInput)
	}
	if len(change.New.Voters) == 0 {
		return fmt.Errorf("%w: new configuration has no voters", ErrInvalidInput)
	}
	if duplicateID(change.New.Voters) || duplicateID(change.New.Learners) {
		return fmt.Errorf("%w: duplicate node in new configuration", ErrInvalidInput)
	}
	if change.Kind == ChangeEnterJoint {
		if len(change.Old.Voters) == 0 {
			return fmt.Errorf("%w: joint change requires old voters", ErrInvalidInput)
		}
		if duplicateID(change.Old.Voters) || duplicateID(change.Old.Learners) {
			return fmt.Errorf("%w: duplicate node in old configuration", ErrInvalidInput)
		}
	}
	return nil
}
func duplicateID(ids []NodeID) bool {
	seen := make(map[NodeID]struct{}, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			return true
		}
		seen[id] = struct{}{}
	}
	return false
}

// buildSegments walks the log and records the effective quorum groups for
// every range of indices. A configuration entry applies at its own index.
func buildSegments(in Input) []segment {
	segments := make([]segment, 0)
	if len(in.Entries) == 0 {
		return segments
	}
	current := in.Initial
	segStart := in.Entries[0].Index
	flush := func(through uint64) {
		if through >= segStart {
			segments = append(segments, segment{from: segStart, groups: singleGroups(current)})
		}
	}
	for _, entry := range in.Entries {
		if entry.Change == nil {
			continue
		}
		change := entry.Change
		flush(entry.Index - 1)
		if change.Kind == ChangeEnterJoint {
			segments = append(segments, segment{
				from:   entry.Index,
				groups: jointGroups(change.Old, change.New),
			})
			current = change.New
		} else {
			segments = append(segments, segment{
				from:   entry.Index,
				groups: singleGroups(change.New),
			})
			current = change.New
		}
		segStart = entry.Index + 1
	}
	lastIndex := in.Entries[len(in.Entries)-1].Index
	flush(lastIndex)
	return segments
}
func singleGroups(config Config) []quorumGroup {
	return []quorumGroup{{name: "", config: config, voters: sortedVoters(config)}}
}
func jointGroups(oldConfig, newConfig Config) []quorumGroup {
	return []quorumGroup{
		{name: "old", config: oldConfig, voters: sortedVoters(oldConfig)},
		{name: "new", config: newConfig, voters: sortedVoters(newConfig)},
	}
}
func sortedVoters(config Config) []NodeID {
	voters := append([]NodeID(nil), config.Voters...)
	sort.Slice(voters, func(i, j int) bool { return voters[i] < voters[j] })
	return voters
}

// effectiveMatches reduces each replica's claimed match index to what it can
// legitimately confirm under its persisted term and the leader's term.
func effectiveMatches(in Input) map[NodeID]uint64 {
	matches := make(map[NodeID]uint64, len(in.Replicas))
	for node, replica := range in.Replicas {
		if replica.PersistedTerm > in.CurrentTerm {
			continue
		}
		matches[node] = clampMatch(in.Entries, replica)
	}
	return matches
}

// clampMatch limits match to the longest prefix whose entries carry terms no
// greater than the replica's persisted term.
func clampMatch(entries []LogEntry, replica ReplicaState) uint64 {
	match := replica.MatchIndex
	if len(entries) == 0 || match < entries[0].Index {
		return 0
	}
	upper := len(entries) - 1
	if match < entries[upper].Index {
		upper = int(match - entries[0].Index)
	}
	// Terms are non-decreasing, so binary search finds the first entry whose
	// term the replica has not persisted.
	firstBad := sort.Search(upper+1, func(i int) bool {
		return entries[i].Term > replica.PersistedTerm
	})
	if firstBad == 0 {
		return entries[0].Index - 1
	}
	if firstBad > upper {
		return match
	}
	badIndex := entries[firstBad].Index
	if badIndex <= match {
		return badIndex - 1
	}
	return match
}

// committedAt reports whether every configuration effective over
// (prevCommit, k] has quorum confirmation at k or above.
func committedAt(k, prevCommit uint64, segments []segment, matches map[NodeID]uint64) bool {
	for _, seg := range segments {
		from := max(seg.from, prevCommit+1)
		if from > k {
			continue
		}
		for _, group := range seg.groups {
			if !hasQuorum(group, k, matches) {
				return false
			}
		}
	}
	return true
}
func hasQuorum(group quorumGroup, target uint64, matches map[NodeID]uint64) bool {
	need := len(group.voters)/2 + 1
	have := 0
	for _, voter := range group.voters {
		if matches[voter] >= target {
			have++
		}
	}
	return have >= need
}
func collectBottlenecks(target, prevCommit uint64, segments []segment, matches map[NodeID]uint64) []Bottleneck {
	bottlenecks := make([]Bottleneck, 0)
	for _, seg := range segments {
		from := max(seg.from, prevCommit+1)
		if from > target {
			continue
		}
		to := target
		for _, group := range seg.groups {
			need := len(group.voters)/2 + 1
			acks := make([]NodeID, 0, len(group.voters))
			for _, voter := range group.voters {
				if matches[voter] >= target {
					acks = append(acks, voter)
				}
			}
			if len(acks) >= need {
				continue
			}
			bottlenecks = append(bottlenecks, Bottleneck{
				FromIndex: from,
				ToIndex:   to,
				Joint:     group.name != "",
				Group:     group.name,
				Config:    group.config,
				Voters:    append([]NodeID(nil), group.voters...),
				Acks:      acks,
				Need:      need,
				Have:      len(acks),
			})
		}
	}
	return bottlenecks
}
func max(a, b uint64) uint64 {
	if a > b {
		return a
	}
	return b
}
