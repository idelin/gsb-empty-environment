package main

import (
	"errors"
	"fmt"
	"sort"
)

// Player is a tournament entrant. ID must be positive and unique.
type Player struct {
	ID   int    `json:"id"`
	Team string `json:"team"`
	Tier int    `json:"tier"`
}

// Config describes one bracket-seeding problem.
//
//	Size      bracket size (power of two, <= 16); 0 means next power of two >= len(Players)
//	Players   entrants (at most Size)
//	SlotTiers optional, one tier label per slot; a player may only occupy a slot
//	          whose tier equals the player's tier
//	Locked    slot index -> player ID, fixes a player into a slot
type Config struct {
	Size      int         `json:"size"`
	Players   []Player    `json:"players"`
	SlotTiers []int       `json:"slotTiers"`
	Locked    map[int]int `json:"locked"`
}

// Problem is a validated, solver-ready Config.
type Problem struct {
	size    int
	rounds  int
	players []Player // sorted by ID
	byID    map[int]int

	slotTier []int   // per slot tier label, valid only if tiered
	tiered   bool    // whether SlotTiers was supplied
	locked   []int   // per slot: locked player index or -1
	cand     [][]int // per slot: candidate player indices, ascending ID
	canEmpty []bool  // per slot: may stay a bye
	empties  int     // number of bye slots

	meet   [][]int // meet[s1][s2] = round (1..rounds) in which slots s1,s2 first meet
	weight []int64 // weight[r] = penalty of a same-team pair first meeting in round r

	teamOf     []int // player index -> team index
	teamName   []string
	teamSize   []int
	totalPairs int64 // total same-team pairs
}

// Solution is an optimal bracket assignment.
type Solution struct {
	assign []int // slot -> player index, -1 = bye
	cost   int64
}

func nextPow2(n int) int {
	p := 1
	for p < n {
		p <<= 1
	}
	return p
}

func log2(n int) int {
	r := 0
	for n > 1 {
		n >>= 1
		r++
	}
	return r
}

// meetRound returns the round (1-indexed) in which two leaf slots first meet,
// assuming both win every match. It is the position of the highest
// differing bit of the two slot indices, plus one.
func meetRound(a, b int) int {
	x := a ^ b
	r := 0
	for x > 0 {
		r++
		x >>= 1
	}
	return r
}

// NewProblem validates cfg and precomputes solver tables.
func NewProblem(cfg *Config) (*Problem, error) {
	if len(cfg.Players) == 0 {
		return nil, errors.New("no players")
	}
	size := cfg.Size
	if size == 0 {
		size = nextPow2(len(cfg.Players))
		if size < 2 {
			size = 2
		}
	}
	if size < 2 || size > 16 || size != nextPow2(size) {
		return nil, fmt.Errorf("size must be a power of two in [2,16], got %d", size)
	}
	if len(cfg.Players) > size {
		return nil, fmt.Errorf("%d players do not fit in a bracket of %d", len(cfg.Players), size)
	}

	players := append([]Player(nil), cfg.Players...)
	sort.Slice(players, func(i, j int) bool { return players[i].ID < players[j].ID })
	byID := make(map[int]int, len(players))
	for i, pl := range players {
		if pl.ID <= 0 {
			return nil, fmt.Errorf("player ID must be positive, got %d", pl.ID)
		}
		if _, dup := byID[pl.ID]; dup {
			return nil, fmt.Errorf("duplicate player ID %d", pl.ID)
		}
		byID[pl.ID] = i
	}

	p := &Problem{
		size:    size,
		rounds:  log2(size),
		players: players,
		byID:    byID,
		empties: size - len(players),
	}

	// Team bookkeeping.
	teamIdx := make(map[string]int)
	p.teamOf = make([]int, len(players))
	for i, pl := range players {
		t, ok := teamIdx[pl.Team]
		if !ok {
			t = len(p.teamName)
			teamIdx[pl.Team] = t
			p.teamName = append(p.teamName, pl.Team)
			p.teamSize = append(p.teamSize, 0)
		}
		p.teamOf[i] = t
		p.teamSize[t]++
	}
	for _, n := range p.teamSize {
		p.totalPairs += int64(n) * int64(n-1) / 2
	}

	// Tiers.
	if len(cfg.SlotTiers) > 0 {
		if len(cfg.SlotTiers) != size {
			return nil, fmt.Errorf("slotTiers length %d != size %d", len(cfg.SlotTiers), size)
		}
		p.tiered = true
		p.slotTier = append([]int(nil), cfg.SlotTiers...)
		need := make(map[int]int)
		have := make(map[int]int)
		for _, t := range p.slotTier {
			need[t]++
		}
		for _, pl := range players {
			have[pl.Tier]++
		}
		for t, n := range need {
			if have[t] != n {
				return nil, fmt.Errorf("tier %d: %d slots but %d players", t, n, have[t])
			}
		}
		for t := range have {
			if need[t] != have[t] {
				return nil, fmt.Errorf("tier %d: %d players but %d slots", t, have[t], need[t])
			}
		}
	}

	// Locks.
	p.locked = make([]int, size)
	for i := range p.locked {
		p.locked[i] = -1
	}
	for slot, id := range cfg.Locked {
		if slot < 0 || slot >= size {
			return nil, fmt.Errorf("locked slot %d out of range [0,%d)", slot, size)
		}
		pi, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("locked player ID %d does not exist", id)
		}
		if p.locked[slot] != -1 {
			return nil, fmt.Errorf("slot %d locked twice", slot)
		}
		if p.tiered && players[pi].Tier != p.slotTier[slot] {
			return nil, fmt.Errorf("locked player %d (tier %d) does not fit slot %d (tier %d)",
				id, players[pi].Tier, slot, p.slotTier[slot])
		}
		p.locked[slot] = pi
	}
	{
		seen := make(map[int]int)
		for slot, pi := range p.locked {
			if pi < 0 {
				continue
			}
			if prev, dup := seen[pi]; dup {
				return nil, fmt.Errorf("player %d locked into both slot %d and slot %d",
					players[pi].ID, prev, slot)
			}
			seen[pi] = slot
		}
	}

	// Per-slot candidates (ascending player ID) and bye eligibility.
	p.cand = make([][]int, size)
	p.canEmpty = make([]bool, size)
	for s := 0; s < size; s++ {
		if p.locked[s] >= 0 {
			p.cand[s] = []int{p.locked[s]}
			continue
		}
		p.canEmpty[s] = true
		for pi := range players {
			if p.tiered && players[pi].Tier != p.slotTier[s] {
				continue
			}
			p.cand[s] = append(p.cand[s], pi)
		}
	}

	// Meet-round table and round weights. Meeting one round later halves the
	// penalty: weight(r) = 2^(rounds-r), so round 1 is the most expensive and
	// the final costs 1.
	p.meet = make([][]int, size)
	for i := range p.meet {
		p.meet[i] = make([]int, size)
		for j := range p.meet[i] {
			p.meet[i][j] = meetRound(i, j)
		}
	}
	p.weight = make([]int64, p.rounds+1)
	for r := 1; r <= p.rounds; r++ {
		p.weight[r] = 1 << uint(p.rounds-r)
	}
	return p, nil
}

func (p *Problem) idOf(pi int) int {
	if pi < 0 {
		return 0
	}
	return p.players[pi].ID
}

// prefixCmp compares the ID sequence of a[0:n] with b[0:n] (bye = 0).
func (p *Problem) prefixCmp(a, b []int, n int) int {
	for i := 0; i < n; i++ {
		ai, bi := p.idOf(a[i]), p.idOf(b[i])
		if ai != bi {
			if ai < bi {
				return -1
			}
			return 1
		}
	}
	return 0
}

// Solve finds a minimum-penalty bracket via depth-first branch and bound over
// slots in order. Ties are broken by the lexicographically smallest player-ID
// sequence across slots 0..size-1 (bye = 0).
func (p *Problem) Solve() Solution {
	var best Solution
	cur := make([]int, p.size)
	used := make([]bool, len(p.players))
	placed := make([]int, len(p.teamName))

	var rec func(slot int, cost, remPairs int64, emptiesLeft int)
	rec = func(slot int, cost, remPairs int64, emptiesLeft int) {
		if best.assign != nil {
			// Lower bound: every not-yet-placed same-team pair still costs
			// at least weight(rounds) = 1.
			if cost+remPairs > best.cost {
				return
			}
			if cost+remPairs == best.cost && p.prefixCmp(cur, best.assign, slot) > 0 {
				return
			}
		}
		if slot == p.size {
			if best.assign == nil || cost < best.cost ||
				(cost == best.cost && p.prefixCmp(cur, best.assign, p.size) < 0) {
				best.assign = append([]int(nil), cur...)
				best.cost = cost
			}
			return
		}
		if p.canEmpty[slot] && emptiesLeft > 0 {
			cur[slot] = -1
			rec(slot+1, cost, remPairs, emptiesLeft-1)
		}
		for _, pi := range p.cand[slot] {
			if used[pi] {
				continue
			}
			used[pi] = true
			cur[slot] = pi
			t := p.teamOf[pi]
			var add int64
			for s2 := 0; s2 < slot; s2++ {
				q := cur[s2]
				if q >= 0 && p.teamOf[q] == t {
					add += p.weight[p.meet[slot][s2]]
				}
			}
			placed[t]++
			rec(slot+1, cost+add, remPairs-int64(placed[t]-1), emptiesLeft)
			placed[t]--
			used[pi] = false
		}
	}
	rec(0, 0, p.totalPairs, p.empties)
	return best
}

// BruteForce enumerates every legal assignment without pruning. It is the
// small-scale cross-check for Solve. count reports complete assignments
// visited; ok is false when the enumeration exceeded limit and aborted.
func (p *Problem) BruteForce(limit int64) (best Solution, count int64, ok bool) {
	cur := make([]int, p.size)
	used := make([]bool, len(p.players))
	ok = true

	var rec func(slot int, cost int64, emptiesLeft int)
	rec = func(slot int, cost int64, emptiesLeft int) {
		if !ok {
			return
		}
		if slot == p.size {
			count++
			if count > limit {
				ok = false
				return
			}
			if best.assign == nil || cost < best.cost ||
				(cost == best.cost && p.prefixCmp(cur, best.assign, p.size) < 0) {
				best.assign = append([]int(nil), cur...)
				best.cost = cost
			}
			return
		}
		if p.canEmpty[slot] && emptiesLeft > 0 {
			cur[slot] = -1
			rec(slot+1, cost, emptiesLeft-1)
		}
		for _, pi := range p.cand[slot] {
			if used[pi] {
				continue
			}
			used[pi] = true
			cur[slot] = pi
			t := p.teamOf[pi]
			var add int64
			for s2 := 0; s2 < slot; s2++ {
				q := cur[s2]
				if q >= 0 && p.teamOf[q] == t {
					add += p.weight[p.meet[slot][s2]]
				}
			}
			rec(slot+1, cost+add, emptiesLeft)
			used[pi] = false
		}
	}
	rec(0, 0, p.empties)
	return best, count, ok
}

// PairReport describes one same-team pair in a solution.
type PairReport struct {
	Team    string
	ID1     int
	ID2     int
	Round   int
	Penalty int64
}

// PairReports lists every same-team pair's earliest meeting round.
func (p *Problem) PairReports(sol Solution) []PairReport {
	pos := make([]int, len(p.players))
	for s, pi := range sol.assign {
		if pi >= 0 {
			pos[pi] = s
		}
	}
	var out []PairReport
	for t, name := range p.teamName {
		var members []int
		for pi := range p.players {
			if p.teamOf[pi] == t {
				members = append(members, pi)
			}
		}
		for i := 0; i < len(members); i++ {
			for j := i + 1; j < len(members); j++ {
				r := p.meet[pos[members[i]]][pos[members[j]]]
				out = append(out, PairReport{
					Team:    name,
					ID1:     p.players[members[i]].ID,
					ID2:     p.players[members[j]].ID,
					Round:   r,
					Penalty: p.weight[r],
				})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Team != out[j].Team {
			return out[i].Team < out[j].Team
		}
		if out[i].ID1 != out[j].ID1 {
			return out[i].ID1 < out[j].ID1
		}
		return out[i].ID2 < out[j].ID2
	})
	return out
}

// RoundLabel names a round: Final, Semifinal, Quarterfinal, Round of N.
func (p *Problem) RoundLabel(round int) string {
	switch p.rounds - round {
	case 0:
		return "Final"
	case 1:
		return "Semifinal"
	case 2:
		return "Quarterfinal"
	default:
		return fmt.Sprintf("Round of %d", 1<<uint(p.rounds-round+1))
	}
}

// Cost recomputes a solution's penalty from scratch (used by tests).
func (p *Problem) Cost(sol Solution) int64 {
	var total int64
	for s1 := 0; s1 < p.size; s1++ {
		for s2 := s1 + 1; s2 < p.size; s2++ {
			a, b := sol.assign[s1], sol.assign[s2]
			if a >= 0 && b >= 0 && p.teamOf[a] == p.teamOf[b] {
				total += p.weight[p.meet[s1][s2]]
			}
		}
	}
	return total
}
