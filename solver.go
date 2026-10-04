package main

import (
	"fmt"
	"sort"
)

// solver is a branch-and-bound search over slot assignments for a fixed
// number of slots. The objective is lexicographic: first minimize the maximum
// individual penalty, then minimize the total penalty.
type solver struct {
	in    *Input
	n     int
	slots int
	table []int

	order        []int    // exam indices, most constrained first
	adj          []uint16 // conflict bitset per exam (n <= 12)
	examStudents [][]int
	studentExams [][]int
	examSize     []int

	assign      []int    // slot per exam, -1 while unassigned
	slotMask    []uint16 // exams currently in each slot
	slotExams   [][]int  // exam indices per slot
	studentSlot [][]int  // [student][slot] -> number of that student's exams there
	pairCount   []int    // adjacent exam pairs formed so far per student

	bestMax    int
	bestTotal  int
	bestAssign []int

	nodes     int
	nodeLimit int
	aborted   bool
}

func newSolver(in *Input, slots int, nodeLimit int) (*solver, error) {
	n := len(in.Exams)
	if n == 0 {
		return nil, fmt.Errorf("没有考试需要安排")
	}
	if n > maxExams {
		return nil, fmt.Errorf("考试数量 %d 超过上限 %d", n, maxExams)
	}
	if slots < 1 {
		return nil, fmt.Errorf("时段数必须为正")
	}
	if len(in.Rooms) == 0 {
		return nil, fmt.Errorf("至少需要一个考场")
	}

	studentIdx := map[string]int{}
	s := &solver{
		in:           in,
		n:            n,
		slots:        slots,
		table:        penaltyTable(in, n),
		adj:          make([]uint16, n),
		examStudents: make([][]int, n),
		examSize:     make([]int, n),
		assign:       make([]int, n),
		slotMask:     make([]uint16, slots),
		slotExams:    make([][]int, slots),
		nodeLimit:    nodeLimit,
	}
	for i, ex := range in.Exams {
		seen := map[int]bool{}
		for _, name := range ex.Students {
			idx, ok := studentIdx[name]
			if !ok {
				idx = len(studentIdx)
				studentIdx[name] = idx
			}
			if !seen[idx] {
				seen[idx] = true
				s.examStudents[i] = append(s.examStudents[i], idx)
			}
		}
		s.examSize[i] = len(s.examStudents[i])
		s.assign[i] = -1
	}
	numStudents := len(studentIdx)
	s.studentExams = make([][]int, numStudents)
	for ei, sts := range s.examStudents {
		for _, st := range sts {
			s.studentExams[st] = append(s.studentExams[st], ei)
		}
	}
	for _, exams := range s.studentExams {
		for a := 0; a < len(exams); a++ {
			for b := a + 1; b < len(exams); b++ {
				s.adj[exams[a]] |= 1 << exams[b]
				s.adj[exams[b]] |= 1 << exams[a]
			}
		}
	}
	s.studentSlot = make([][]int, numStudents)
	for i := range s.studentSlot {
		s.studentSlot[i] = make([]int, slots)
	}
	s.pairCount = make([]int, numStudents)

	degree := make([]int, n)
	for i := 0; i < n; i++ {
		for j := 0; j < n; j++ {
			if s.adj[i]&(1<<j) != 0 {
				degree[i]++
			}
		}
	}
	s.order = make([]int, n)
	for i := range s.order {
		s.order[i] = i
	}
	sort.Slice(s.order, func(a, b int) bool {
		ea, eb := s.order[a], s.order[b]
		if degree[ea] != degree[eb] {
			return degree[ea] > degree[eb]
		}
		return s.examSize[ea] > s.examSize[eb]
	})

	s.bestMax = int(^uint(0) >> 1)
	s.bestTotal = s.bestMax
	return s, nil
}

// objective returns (maxPenalty, totalPenalty) for a complete assignment.
func (s *solver) objective() (int, int) {
	maxPen, total := 0, 0
	for _, exams := range s.studentExams {
		slots := make([]int, len(exams))
		for i, e := range exams {
			slots[i] = s.assign[e]
		}
		p := studentPenalty(slots, s.in.SlotsPerDay, s.table)
		if p > maxPen {
			maxPen = p
		}
		total += p
	}
	return maxPen, total
}

func lexLess(aMax, aTotal, bMax, bTotal int) bool {
	return aMax < bMax || (aMax == bMax && aTotal < bTotal)
}

// seedGreedy produces an initial incumbent so branch-and-bound prunes early.
// Whatever it places is always unplaced again before returning.
func (s *solver) seedGreedy() {
	placed := 0
	for _, e := range s.order {
		bestSlot, bestPairs := -1, int(^uint(0)>>1)
		for slot := 0; slot < s.slots; slot++ {
			if s.slotMask[slot]&s.adj[e] != 0 {
				continue
			}
			pairs := s.pairsAdded(e, slot)
			if pairs < bestPairs && s.roomsOK(e, slot) {
				bestPairs, bestSlot = pairs, slot
			}
		}
		if bestSlot < 0 {
			break // greedy failed; incumbent stays at +inf
		}
		s.place(e, bestSlot)
		placed++
	}
	if placed == s.n {
		maxPen, total := s.objective()
		s.bestMax, s.bestTotal = maxPen, total
		s.bestAssign = append([]int(nil), s.assign...)
	}
	for i := 0; i < placed; i++ {
		e := s.order[i]
		s.unplace(e, s.assign[e])
	}
}

// pairsAdded counts how many new adjacent exam pairs placing exam e in the
// given slot would create (both endpoints already assigned).
func (s *solver) pairsAdded(e, slot int) int {
	pairs := 0
	pd := s.in.SlotsPerDay
	for _, st := range s.examStudents[e] {
		if slot > 0 && adjacentSlots(slot-1, slot, pd) {
			pairs += s.studentSlot[st][slot-1]
		}
		if slot+1 < s.slots && adjacentSlots(slot, slot+1, pd) {
			pairs += s.studentSlot[st][slot+1]
		}
	}
	return pairs
}

func (s *solver) place(e, slot int) {
	s.assign[e] = slot
	s.slotMask[slot] |= 1 << e
	s.slotExams[slot] = append(s.slotExams[slot], e)
	for _, st := range s.examStudents[e] {
		s.studentSlot[st][slot]++
	}
}

func (s *solver) unplace(e, slot int) {
	s.assign[e] = -1
	s.slotMask[slot] &^= 1 << e
	lst := s.slotExams[slot]
	for i, x := range lst {
		if x == e {
			s.slotExams[slot] = append(lst[:i], lst[i+1:]...)
			break
		}
	}
	for _, st := range s.examStudents[e] {
		s.studentSlot[st][slot]--
	}
}

// roomsOK checks whether the exams currently in a slot (plus candidate exam e)
// can still be matched to rooms.
func (s *solver) roomsOK(e, slot int) bool {
	exams := append(append([]int(nil), s.slotExams[slot]...), e)
	sizes := make([]int, len(exams))
	for i, x := range exams {
		sizes[i] = s.examSize[x]
	}
	return assignRooms(sizes, s.in.Rooms) != nil
}

// bound returns lower bounds (max, total) on the final objective given the
// partial assignment. Every adjacent pair already formed stays formed, and a
// run of k+1 exams costs k(k+1)/2 >= k, so each pair contributes at least 1.
func (s *solver) bound() (int, int) {
	lbMax, lbTotal := 0, 0
	for _, c := range s.pairCount {
		if c > lbMax {
			lbMax = c
		}
		lbTotal += c
	}
	return lbMax, lbTotal
}

func (s *solver) search(k int) {
	if s.aborted {
		return
	}
	if s.nodeLimit > 0 && s.nodes >= s.nodeLimit {
		s.aborted = true
		return
	}
	s.nodes++
	if k == s.n {
		maxPen, total := s.objective()
		if lexLess(maxPen, total, s.bestMax, s.bestTotal) {
			s.bestMax, s.bestTotal = maxPen, total
			s.bestAssign = append([]int(nil), s.assign...)
		}
		return
	}
	e := s.order[k]
	for slot := 0; slot < s.slots; slot++ {
		if s.slotMask[slot]&s.adj[e] != 0 {
			continue
		}
		if !s.roomsOK(e, slot) {
			continue
		}
		s.place(e, slot)
		for _, st := range s.examStudents[e] {
			s.pairCount[st] += pairsOf(s.studentSlot[st], slot, s.in.SlotsPerDay)
		}
		lbMax, lbTotal := s.bound()
		if !(lbMax > s.bestMax || (lbMax == s.bestMax && lbTotal >= s.bestTotal)) {
			s.search(k + 1)
		}
		for _, st := range s.examStudents[e] {
			s.pairCount[st] -= pairsOf(s.studentSlot[st], slot, s.in.SlotsPerDay)
		}
		s.unplace(e, slot)
	}
}

// pairsOf counts adjacent pairs between the given slot and its neighbours for
// one student, using the current studentSlot counts.
func pairsOf(counts []int, slot, slotsPerDay int) int {
	pairs := 0
	if slot > 0 && adjacentSlots(slot-1, slot, slotsPerDay) {
		pairs += counts[slot-1]
	}
	if slot+1 < len(counts) && adjacentSlots(slot, slot+1, slotsPerDay) {
		pairs += counts[slot+1]
	}
	return pairs
}

// solveFixed solves the problem for a fixed number of slots. Returns nil if
// infeasible.
func solveFixed(in *Input, slots, nodeLimit int) (*Solution, error) {
	s, err := newSolver(in, slots, nodeLimit)
	if err != nil {
		return nil, err
	}
	s.seedGreedy()
	s.search(0)
	if s.bestAssign == nil {
		return nil, nil
	}
	return s.buildSolution(), nil
}

func (s *solver) buildSolution() *Solution {
	pd := s.in.SlotsPerDay
	sol := &Solution{
		Slots:        s.slots,
		MaxPenalty:   s.bestMax,
		TotalPenalty: s.bestTotal,
		Proven:       !s.aborted,
	}
	roomOf := map[int]int{} // exam -> room index
	for slot := 0; slot < s.slots; slot++ {
		exams := s.slotExamsOf(slot)
		sizes := make([]int, len(exams))
		for i, e := range exams {
			sizes[i] = s.examSize[e]
		}
		rooms := assignRooms(sizes, s.in.Rooms)
		for i, e := range exams {
			roomOf[e] = rooms[i]
		}
	}
	for e, ex := range s.in.Exams {
		slot := s.bestAssign[e]
		p := Placement{Exam: ex.ID, Slot: slot, Room: s.in.Rooms[roomOf[e]].Name, Enrollment: s.examSize[e]}
		if pd > 0 {
			p.Day = slot / pd
			p.SlotInDay = slot % pd
		} else {
			p.Day = 0
			p.SlotInDay = slot
		}
		sol.Placements = append(sol.Placements, p)
	}
	names := make([]string, len(s.studentExams))
	// Recover student names in index order.
	idxName := map[int]string{}
	// studentIdx map was local to newSolver; rebuild from input.
	{
		next := 0
		seen := map[string]int{}
		for _, ex := range s.in.Exams {
			for _, name := range ex.Students {
				if _, ok := seen[name]; !ok {
					seen[name] = next
					next++
				}
			}
		}
		for name, idx := range seen {
			idxName[idx] = name
		}
	}
	for i := range names {
		names[i] = idxName[i]
	}
	for st, exams := range s.studentExams {
		slots := make([]int, len(exams))
		for i, e := range exams {
			slots[i] = s.bestAssign[e]
		}
		p := studentPenalty(slots, pd, s.table)
		sol.PerStudent = append(sol.PerStudent, StudentPenalty{Student: names[st], Slots: slots, Penalty: p})
	}
	sort.Slice(sol.PerStudent, func(a, b int) bool {
		if sol.PerStudent[a].Penalty != sol.PerStudent[b].Penalty {
			return sol.PerStudent[a].Penalty > sol.PerStudent[b].Penalty
		}
		return sol.PerStudent[a].Student < sol.PerStudent[b].Student
	})
	return sol
}

// slotExamsOf reconstructs the exam list of a slot from the best assignment.
func (s *solver) slotExamsOf(slot int) []int {
	var exams []int
	for e := 0; e < s.n; e++ {
		if s.bestAssign[e] == slot {
			exams = append(exams, e)
		}
	}
	return exams
}

// Solve picks the slot count (auto when input.Slots == 0) and returns the
// best solution found. With auto slots the objective is non-increasing in the
// slot count, so the search stops as soon as the objective reaches (0, 0).
func Solve(in *Input, nodeLimit int) (*Solution, error) {
	if len(in.Exams) > maxExams {
		return nil, fmt.Errorf("考试数量 %d 超过上限 %d", len(in.Exams), maxExams)
	}
	if in.Slots > 0 {
		sol, err := solveFixed(in, in.Slots, nodeLimit)
		if err != nil {
			return nil, err
		}
		if sol == nil {
			return nil, fmt.Errorf("在 %d 个时段下不存在可行安排（考场或冲突约束过紧）", in.Slots)
		}
		return sol, nil
	}
	// Auto: lower bound is the largest exam load of any single student
	// (those exams form a clique in the conflict graph).
	lb := 1
	load := map[string]int{}
	for _, ex := range in.Exams {
		for _, st := range ex.Students {
			load[st]++
			if load[st] > lb {
				lb = load[st]
			}
		}
	}
	var best *Solution
	for slots := lb; slots <= len(in.Exams); slots++ {
		sol, err := solveFixed(in, slots, nodeLimit)
		if err != nil {
			return nil, err
		}
		if sol == nil {
			continue
		}
		if best == nil || lexLess(sol.MaxPenalty, sol.TotalPenalty, best.MaxPenalty, best.TotalPenalty) {
			best = sol
		}
		if sol.MaxPenalty == 0 && sol.TotalPenalty == 0 {
			break
		}
	}
	if best == nil {
		return nil, fmt.Errorf("不存在可行安排（考场或冲突约束过紧）")
	}
	return best, nil
}
