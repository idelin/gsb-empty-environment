package maxcsp

import (
	"fmt"
	"sort"
	"strings"
)

// Task scheduling scenario used as the acceptance example.
//
// Five tasks T1..T5 must each be placed into a distinct time slot in
// {1..5}. Three hard constraints must always hold; five soft constraints
// carry penalty weights. The instance is designed so that no assignment can
// satisfy every soft constraint: the unique optimum violates exactly the two
// minor soft constraints S3 and S4 for a total penalty of 7.
//
//	H1 (hard): no two tasks share a slot
//	H2 (hard): T1 finishes before T3 starts   slot(T1) < slot(T3)
//	H3 (hard): T5 meets its deadline          slot(T5) <= 2
//	S1 (w=10): T2 gets the first slot         slot(T2) == 1
//	S2 (w=8):  T4 immediately follows T1      slot(T4) == slot(T1)+1
//	S3 (w=4):  T3 lands on an even slot       slot(T3) % 2 == 0
//	S4 (w=3):  T4 starts early                slot(T4) <= 3
//	S5 (w=2):  T2 precedes T4                 slot(T2) < slot(T4)
func NewSchedulingProblem() *Problem {
	vars := []Variable{}
	for _, t := range []string{"T1", "T2", "T3", "T4", "T5"} {
		vars = append(vars, Variable{Name: t, Domain: []int{1, 2, 3, 4, 5}})
	}

	// slots extracts the slots of the named tasks; ok=false while any of
	// them is still unassigned (constraint not yet decidable).
	slots := func(assign map[string]int, names ...string) (vals []int, ok bool) {
		for _, n := range names {
			v, found := assign[n]
			if !found {
				return nil, false
			}
			vals = append(vals, v)
		}
		return vals, true
	}

	tasks := []string{"T1", "T2", "T3", "T4", "T5"}
	constraints := []Constraint{
		{
			Name: "H1-no-overlap", Hard: true,
			Violated: func(a map[string]int) (bool, bool) {
				s, ok := slots(a, tasks...)
				if !ok {
					return false, false
				}
				seen := map[int]bool{}
				for _, v := range s {
					if seen[v] {
						return true, true
					}
					seen[v] = true
				}
				return false, true
			},
		},
		{
			Name: "H2-T1-before-T3", Hard: true,
			Violated: func(a map[string]int) (bool, bool) {
				s, ok := slots(a, "T1", "T3")
				if !ok {
					return false, false
				}
				return s[0] >= s[1], true
			},
		},
		{
			Name: "H3-T5-deadline", Hard: true,
			Violated: func(a map[string]int) (bool, bool) {
				s, ok := slots(a, "T5")
				if !ok {
					return false, false
				}
				return s[0] > 2, true
			},
		},
		{
			Name: "S1-T2-first-slot", Weight: 10,
			Violated: func(a map[string]int) (bool, bool) {
				s, ok := slots(a, "T2")
				if !ok {
					return false, false
				}
				return s[0] != 1, true
			},
		},
		{
			Name: "S2-T4-follows-T1", Weight: 8,
			Violated: func(a map[string]int) (bool, bool) {
				s, ok := slots(a, "T1", "T4")
				if !ok {
					return false, false
				}
				return s[1] != s[0]+1, true
			},
		},
		{
			Name: "S3-T3-even-slot", Weight: 4,
			Violated: func(a map[string]int) (bool, bool) {
				s, ok := slots(a, "T3")
				if !ok {
					return false, false
				}
				return s[0]%2 != 0, true
			},
		},
		{
			Name: "S4-T4-early", Weight: 3,
			Violated: func(a map[string]int) (bool, bool) {
				s, ok := slots(a, "T4")
				if !ok {
					return false, false
				}
				return s[0] > 3, true
			},
		},
		{
			Name: "S5-T2-before-T4", Weight: 2,
			Violated: func(a map[string]int) (bool, bool) {
				s, ok := slots(a, "T2", "T4")
				if !ok {
					return false, false
				}
				return s[0] >= s[1], true
			},
		},
	}
	return &Problem{Variables: vars, Constraints: constraints}
}

// Describe renders an assignment as a slot-ordered schedule, e.g.
// "slot1: T2 | slot2: T5 | slot3: T1 | slot4: T4 | slot5: T3".
func Describe(assign map[string]int) string {
	bySlot := map[int]string{}
	slots := []int{}
	for task, slot := range assign {
		bySlot[slot] = task
		slots = append(slots, slot)
	}
	sort.Ints(slots)
	parts := []string{}
	for _, s := range slots {
		parts = append(parts, fmt.Sprintf("slot%d: %s", s, bySlot[s]))
	}
	return strings.Join(parts, " | ")
}
