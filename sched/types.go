// Package sched solves experiment-selection problems with device cooldowns
// and mutual-exclusion groups, and two-stage observation-based variants.
package sched

import "sort"

// Candidate describes one experiment occupying a single integer time slot.
// Profit may be negative. Group, when non-empty, marks a mutual-exclusion
// group: at most one member of a group may be selected.
type Candidate struct {
	ID     string `json:"id"`
	Device string `json:"device"`
	Slot   int    `json:"slot"`
	Profit int    `json:"profit"`
	Group  string `json:"group,omitempty"`
}

// Instance is the single-stage selection problem. Cooldown c means two
// experiments on the same device must leave at least c full slots between
// them, i.e. their slots must differ by at least c+1.
type Instance struct {
	Cooldown   int         `json:"cooldown"`
	Candidates []Candidate `json:"candidates"`
}

// Solution is an optimal legal subset.
type Solution struct {
	Profit int      `json:"profit"`
	IDs    []string `json:"ids"`
}

type event struct {
	slot   int
	device string
	group  string
	id     string
}

// ReplayLegal validates a chosen subset by replaying the timeline in slot
// order: on each device, consecutive experiments must leave at least
// Cooldown full slots between them, and each mutex group may fire at most
// once.
func ReplayLegal(inst *Instance, chosen []int) bool {
	evs := make([]event, 0, len(chosen))
	seen := make(map[int]bool)
	for _, ci := range chosen {
		if ci < 0 || ci >= len(inst.Candidates) || seen[ci] {
			return false
		}
		seen[ci] = true
		c := inst.Candidates[ci]
		evs = append(evs, event{slot: c.Slot, device: c.Device, group: c.Group, id: c.ID})
	}
	sort.Slice(evs, func(i, j int) bool {
		if evs[i].slot != evs[j].slot {
			return evs[i].slot < evs[j].slot
		}
		return evs[i].id < evs[j].id
	})
	lastSlot := make(map[string]int)
	usedGroup := make(map[string]bool)
	for _, e := range evs {
		if e.group != "" {
			if usedGroup[e.group] {
				return false
			}
			usedGroup[e.group] = true
		}
		if prev, ok := lastSlot[e.device]; ok {
			if e.slot-prev < inst.Cooldown+1 {
				return false
			}
		}
		lastSlot[e.device] = e.slot
	}
	return true
}

func profitOf(inst *Instance, chosen []int) int {
	total := 0
	for _, ci := range chosen {
		total += inst.Candidates[ci].Profit
	}
	return total
}

func idsOf(inst *Instance, chosen []int) []string {
	ids := make([]string, 0, len(chosen))
	for _, ci := range chosen {
		ids = append(ids, inst.Candidates[ci].ID)
	}
	sort.Strings(ids)
	return ids
}

// better reports whether (profitA, idsA) beats (profitB, idsB): higher
// profit, then fewer experiments, then lexicographically smaller sorted
// id list.
func better(profitA int, idsA []string, profitB int, idsB []string) bool {
	if profitA != profitB {
		return profitA > profitB
	}
	if len(idsA) != len(idsB) {
		return len(idsA) < len(idsB)
	}
	for i := range idsA {
		if idsA[i] != idsB[i] {
			return idsA[i] < idsB[i]
		}
	}
	return false
}
