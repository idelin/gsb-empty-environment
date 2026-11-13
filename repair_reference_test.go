package netsec

import (
	"errors"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

type brutePlan struct {
	moves []Move
}

func witnessSetFromPlacement(pages []Page, physical map[PageID]PhysicalPage, coords map[PhysicalPage]Location) map[[3]int]bool {
	out := make(map[[3]int]bool)
	for _, sensitive := range pages {
		if sensitive.Role != RoleSensitive {
			continue
		}
		centre, ok := coords[physical[sensitive.ID]]
		if !ok {
			continue
		}
		for _, below := range pages {
			if below.ID == sensitive.ID || below.Role != RoleAttacker {
				continue
			}
			belowLoc, ok := coords[physical[below.ID]]
			if !ok || belowLoc.Channel != centre.Channel || belowLoc.Bank != centre.Bank ||
				belowLoc.Row+1 != centre.Row {
				continue
			}
			for _, above := range pages {
				if above.ID == sensitive.ID || above.ID == below.ID || above.Role != RoleAttacker {
					continue
				}
				aboveLoc, ok := coords[physical[above.ID]]
				if ok && aboveLoc.Channel == centre.Channel && aboveLoc.Bank == centre.Bank &&
					aboveLoc.Row == centre.Row+1 {
					out[[3]int{sensitive.ID, below.ID, above.ID}] = true
				}
			}
		}
	}
	return out
}

func strictOrderExists(plan brutePlan, pages []Page, coords map[PhysicalPage]Location) bool {
	initial := make(map[PageID]PhysicalPage, len(pages))
	byID := make(map[PageID]Page, len(pages))
	for _, p := range pages {
		initial[p.ID] = p.Physical
		byID[p.ID] = p
	}
	base := witnessSetFromPlacement(pages, initial, coords)

	var search func(done int, current map[PageID]PhysicalPage, previous map[[3]int]bool, used []bool) bool
	search = func(done int, current map[PageID]PhysicalPage, previous map[[3]int]bool, used []bool) bool {
		if done == len(plan.moves) {
			return len(witnessSetFromPlacement(pages, current, coords)) == 0
		}
		for i, move := range plan.moves {
			if used[i] {
				continue
			}
			current[move.Page] = move.To
			now := witnessSetFromPlacement(pages, current, coords)
			subset := true
			for witness := range now {
				if !previous[witness] {
					subset = false
					break
				}
			}
			if subset {
				used[i] = true
				if search(done+1, current, now, used) {
					return true
				}
				used[i] = false
			}
			current[move.Page] = initial[move.Page]
		}
		return false
	}
	return search(0, clonePlacement(initial), base, make([]bool, len(plan.moves)))
}

func clonePlacement(in map[PageID]PhysicalPage) map[PageID]PhysicalPage {
	out := make(map[PageID]PhysicalPage, len(in))
	for id, physical := range in {
		out[id] = physical
	}
	return out
}

func planLess(a, b brutePlan) bool {
	for i := 0; i < len(a.moves) && i < len(b.moves); i++ {
		if a.moves[i].Page != b.moves[i].Page {
			return a.moves[i].Page < b.moves[i].Page
		}
		if a.moves[i].To != b.moves[i].To {
			return a.moves[i].To < b.moves[i].To
		}
	}
	return false
}

func bruteRepair(pages []Page, coords map[PhysicalPage]Location, free []PhysicalPage) (best *brutePlan, staticFound bool) {
	movable := make([]Page, 0)
	for _, p := range pages {
		if p.Movable && (p.Role == RoleAttacker || p.Role == RoleSensitive) {
			movable = append(movable, p)
		}
	}
	sort.Slice(movable, func(i, j int) bool { return movable[i].ID < movable[j].ID })

	initial := make(map[PageID]PhysicalPage, len(pages))
	for _, p := range pages {
		initial[p.ID] = p.Physical
	}

	var all []brutePlan
	var assign func(i int, current map[PageID]PhysicalPage, usedFree map[PhysicalPage]bool, moves []Move)
	assign = func(i int, current map[PageID]PhysicalPage, usedFree map[PhysicalPage]bool, moves []Move) {
		if i == len(movable) {
			if len(witnessSetFromPlacement(pages, current, coords)) != 0 {
				return
			}
			planMoves := append([]Move{}, moves...)
			plan := brutePlan{moves: planMoves}
			sort.Slice(plan.moves, func(a, b int) bool { return plan.moves[a].Page < plan.moves[b].Page })
			all = append(all, plan)
			staticFound = true
			return
		}
		p := movable[i]
		assign(i+1, current, usedFree, moves)
		for _, target := range free {
			if usedFree[target] {
				continue
			}
			usedFree[target] = true
			current[p.ID] = target
			moves = append(moves, Move{Page: p.ID, From: p.Physical, To: target})
			assign(i+1, current, usedFree, moves)
			moves = moves[:len(moves)-1]
			current[p.ID] = p.Physical
			delete(usedFree, target)
		}
	}
	assign(0, clonePlacement(initial), make(map[PhysicalPage]bool), nil)

	for i := range all {
		if !strictOrderExists(all[i], pages, coords) {
			continue
		}
		if best == nil || len(all[i].moves) < len(best.moves) ||
			(len(all[i].moves) == len(best.moves) && planLess(all[i], *best)) {
			copyPlan := brutePlan{moves: append([]Move{}, all[i].moves...)}
			best = &copyPlan
		}
	}
	return best, staticFound
}

func TestRepairAgainstBruteForceReference(t *testing.T) {
	rng := rand.New(rand.NewSource(0xc0ffee))
	for iteration := 0; iteration < 500; iteration++ {
		limits := Limits{Channels: 2, Banks: 2, Rows: 6}
		coords := make(map[PhysicalPage]Location)
		pages := make([]Page, 0, 8)
		for id := 0; id < 8; id++ {
			if rng.Intn(7) == 0 {
				continue
			}
			coords[id] = Location{
				Channel: rng.Intn(limits.Channels),
				Bank:    rng.Intn(limits.Banks),
				Row:     rng.Intn(limits.Rows),
			}
			role := RoleOrdinary
			switch rng.Intn(3) {
			case 1:
				role = RoleAttacker
			case 2:
				role = RoleSensitive
			}
			pages = append(pages, testPage(id, id, role, rng.Intn(2) == 0))
		}
		freeCount := rng.Intn(4)
		free := make([]PhysicalPage, 0, freeCount)
		for i := 0; i < freeCount; i++ {
			physical := 100 + i
			free = append(free, physical)
			coords[physical] = Location{
				Channel: rng.Intn(limits.Channels),
				Bank:    rng.Intn(limits.Banks),
				Row:     rng.Intn(limits.Rows),
			}
		}
		layout := Layout{Pages: pages, FreePages: free, Limits: limits, Locate: testLocate(coords)}

		want, staticFound := bruteRepair(pages, coords, free)
		result, err := Repair(layout, &RepairOptions{Budget: 500_000})
		switch {
		case want != nil:
			if err != nil {
				t.Fatalf("iteration %d: unexpected error %v (want plan %+v)", iteration, err, want.moves)
			}
			if !reflect.DeepEqual(result.Moves, want.moves) {
				t.Fatalf("iteration %d: moves=%+v want=%+v\npages=%+v\ncoords=%+v\nfree=%v",
					iteration, result.Moves, want.moves, pages, coords, free)
			}
			if !result.Optimal || !result.Online {
				t.Fatalf("iteration %d: plan not closed: %+v", iteration, result)
			}
		case staticFound:
			if !errors.Is(err, ErrNoOnlineOrder) {
				t.Fatalf("iteration %d: want ErrNoOnlineOrder, got %v", iteration, err)
			}
		default:
			if !errors.Is(err, ErrNoSafePlacement) {
				t.Fatalf("iteration %d: want ErrNoSafePlacement, got %v", iteration, err)
			}
		}
	}
}
