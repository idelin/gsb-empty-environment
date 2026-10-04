package main

import (
	"math/rand"
	"testing"
)

// findAssignment 返回指定预约的接纳结果，未接纳返回 nil。
func findAssignment(res *Result, id string) *Assignment {
	for i := range res.Accepted {
		if res.Accepted[i].ReservationID == id {
			return &res.Accepted[i]
		}
	}
	return nil
}

func findRejection(res *Result, id string) *Rejection {
	for i := range res.Rejected {
		if res.Rejected[i].ReservationID == id {
			return &res.Rejected[i]
		}
	}
	return nil
}

// TestHalfOpenTouching 验证左闭右开规则：首尾相接不算冲突。
func TestHalfOpenTouching(t *testing.T) {
	p := &Problem{
		Spots: []Spot{{ID: "S1", Types: []string{"sedan"}}},
		Reservations: []Reservation{
			{ID: "A", VehicleType: "sedan", Arrival: 0, Departure: 60, Revenue: 10},
			{ID: "B", VehicleType: "sedan", Arrival: 60, Departure: 120, Revenue: 10},
		},
	}
	res, err := Solve(p)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if res.TotalRevenue != 20 || len(res.Accepted) != 2 {
		t.Fatalf("touching intervals must both be accepted, got %+v", res)
	}
}

// TestBuffersCreateConflict 验证缓冲被计入占用时间。
func TestBuffersCreateConflict(t *testing.T) {
	p := &Problem{
		EntryBuffer: 5,
		ExitBuffer:  5,
		Spots:       []Spot{{ID: "S1", Types: []string{"sedan"}}},
		Reservations: []Reservation{
			{ID: "A", VehicleType: "sedan", Arrival: 0, Departure: 60, Revenue: 10},
			// 表面间隔 8 分钟，但 A 的离场缓冲 5 + B 的入场缓冲 5 = 10 > 8，冲突。
			{ID: "B", VehicleType: "sedan", Arrival: 68, Departure: 120, Revenue: 10},
		},
	}
	res, err := Solve(p)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if len(res.Accepted) != 1 || len(res.Rejected) != 1 {
		t.Fatalf("expected exactly one accepted, got %+v", res)
	}
	// 缓冲后恰好相接（[−5,65) 与 [65,125)）则不冲突。
	p.Reservations[1].Arrival = 70
	res, err = Solve(p)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if len(res.Accepted) != 2 {
		t.Fatalf("buffered intervals touching must both be accepted, got %+v", res)
	}
}

// TestConfirmedCannotBeRevoked 已确认预约即使收益低也必须保留。
func TestConfirmedCannotBeRevoked(t *testing.T) {
	p := &Problem{
		Spots: []Spot{{ID: "S1", Types: []string{"sedan"}}},
		Reservations: []Reservation{
			{ID: "C", VehicleType: "sedan", Arrival: 0, Departure: 100, Revenue: 1, Confirmed: true},
			{ID: "X", VehicleType: "sedan", Arrival: 50, Departure: 150, Revenue: 100},
		},
	}
	res, err := Solve(p)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if findAssignment(res, "C") == nil {
		t.Fatalf("confirmed reservation must be accepted, got %+v", res)
	}
	if findRejection(res, "X") == nil {
		t.Fatalf("conflicting unconfirmed reservation must be rejected, got %+v", res)
	}
	if res.TotalRevenue != 1 {
		t.Fatalf("total revenue = %d, want 1", res.TotalRevenue)
	}
}

// TestTypeEligibility 不同车型只能停入接纳该车型的车位。
func TestTypeEligibility(t *testing.T) {
	p := &Problem{
		Spots: []Spot{
			{ID: "S1", Types: []string{"sedan"}},
			{ID: "S2", Types: []string{"truck"}},
		},
		Reservations: []Reservation{
			{ID: "T1", VehicleType: "truck", Arrival: 0, Departure: 60, Revenue: 10},
			{ID: "T2", VehicleType: "truck", Arrival: 0, Departure: 60, Revenue: 10},
			{ID: "S", VehicleType: "sedan", Arrival: 0, Departure: 60, Revenue: 10},
		},
	}
	res, err := Solve(p)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if res.TotalRevenue != 20 {
		t.Fatalf("total revenue = %d, want 20", res.TotalRevenue)
	}
	a := findAssignment(res, "T1")
	if a == nil || a.SpotID != "S2" {
		t.Fatalf("truck must go to S2, got %+v", res.Accepted)
	}
	if rej := findRejection(res, "T2"); rej == nil || len(rej.Conflicts) != 1 || rej.Conflicts[0].SpotID != "S2" {
		t.Fatalf("T2 rejection must cite conflict on S2, got %+v", res.Rejected)
	}
}

// TestNoEligibleSpot 无可用车型的车位时直接回绝并说明原因。
func TestNoEligibleSpot(t *testing.T) {
	p := &Problem{
		Spots: []Spot{{ID: "S1", Types: []string{"sedan"}}},
		Reservations: []Reservation{
			{ID: "B", VehicleType: "bus", Arrival: 0, Departure: 60, Revenue: 10},
		},
	}
	res, err := Solve(p)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	rej := findRejection(res, "B")
	if rej == nil || rej.Reason == "" {
		t.Fatalf("expected rejection with reason, got %+v", res)
	}
}

// TestTooManyReservations 超过 25 单必须报错。
func TestTooManyReservations(t *testing.T) {
	p := &Problem{Spots: []Spot{{ID: "S1", Types: []string{"sedan"}}}}
	for i := 0; i < MaxReservations+1; i++ {
		p.Reservations = append(p.Reservations, Reservation{
			ID: string(rune('a' + i)), VehicleType: "sedan", Arrival: 0, Departure: 1, Revenue: 1,
		})
	}
	if _, err := Solve(p); err == nil {
		t.Fatal("expected error for more than 25 reservations")
	}
}

// TestConfirmedInfeasible 已确认预约互相冲突且车位不足时必须报错。
func TestConfirmedInfeasible(t *testing.T) {
	p := &Problem{
		Spots: []Spot{{ID: "S1", Types: []string{"sedan"}}},
		Reservations: []Reservation{
			{ID: "A", VehicleType: "sedan", Arrival: 0, Departure: 60, Revenue: 1, Confirmed: true},
			{ID: "B", VehicleType: "sedan", Arrival: 30, Departure: 90, Revenue: 1, Confirmed: true},
		},
	}
	if _, err := Solve(p); err == nil {
		t.Fatal("expected infeasible error for conflicting confirmed reservations")
	}
}

// bruteForceBest 独立实现：枚举所有包含全部已确认预约的子集，
// 用回溯判定能否分配车位，返回最大收益。用于交叉验证求解器最优性。
func bruteForceBest(p *Problem) (int64, bool) {
	n := len(p.Reservations)
	eligible := make([][]int, n)
	occ := make([]interval, n)
	for i, r := range p.Reservations {
		eligible[i] = eligibleSpots(p, r.VehicleType)
		occ[i] = p.occupy(r)
	}
	assignable := func(subset []int) bool {
		busy := make([][]interval, len(p.Spots))
		var rec func(k int) bool
		rec = func(k int) bool {
			if k == len(subset) {
				return true
			}
			i := subset[k]
			for _, sp := range eligible[i] {
				ok := true
				for _, b := range busy[sp] {
					if overlaps(b, occ[i]) {
						ok = false
						break
					}
				}
				if ok {
					busy[sp] = append(busy[sp], occ[i])
					if rec(k + 1) {
						return true
					}
					busy[sp] = busy[sp][:len(busy[sp])-1]
				}
			}
			return false
		}
		return rec(0)
	}
	best := int64(-1)
	found := false
	for mask := 0; mask < (1 << uint(n)); mask++ {
		var subset []int
		var rev int64
		ok := true
		for i := 0; i < n; i++ {
			if mask&(1<<uint(i)) != 0 {
				subset = append(subset, i)
				rev += p.Reservations[i].Revenue
			} else if p.Reservations[i].Confirmed {
				ok = false
				break
			}
		}
		if !ok || !assignable(subset) {
			continue
		}
		found = true
		if rev > best {
			best = rev
		}
	}
	return best, found
}

// TestOptimalityAgainstBruteForce 随机小案例上与暴力枚举对拍。
func TestOptimalityAgainstBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	types := []string{"sedan", "suv", "truck"}
	for trial := 0; trial < 300; trial++ {
		nSpots := 1 + rng.Intn(3)
		p := &Problem{
			EntryBuffer: int64(rng.Intn(3)) * 5,
			ExitBuffer:  int64(rng.Intn(3)) * 5,
		}
		for i := 0; i < nSpots; i++ {
			var ts []string
			for _, ty := range types {
				if rng.Intn(2) == 0 {
					ts = append(ts, ty)
				}
			}
			if len(ts) == 0 {
				ts = []string{types[rng.Intn(len(types))]}
			}
			p.Spots = append(p.Spots, Spot{ID: string(rune('A' + i)), Types: ts})
		}
		n := 1 + rng.Intn(9)
		for i := 0; i < n; i++ {
			start := int64(rng.Intn(120))
			p.Reservations = append(p.Reservations, Reservation{
				ID:          string(rune('a' + i)),
				VehicleType: types[rng.Intn(len(types))],
				Arrival:     start,
				Departure:   start + int64(10+rng.Intn(50)),
				Revenue:     int64(rng.Intn(20)),
				Confirmed:   rng.Intn(5) == 0,
			})
		}
		res, err := Solve(p)
		best, feasible := bruteForceBest(p)
		if feasible != (err == nil) {
			t.Fatalf("trial %d: feasibility mismatch: solver err=%v, brute feasible=%v\nproblem=%+v", trial, err, feasible, p)
		}
		if !feasible {
			continue
		}
		if res.TotalRevenue != best {
			t.Fatalf("trial %d: solver revenue %d != brute force %d\nproblem=%+v", trial, res.TotalRevenue, best, p)
		}
		// 校验结果内部一致性：已确认全部接纳、无冲突、车位合法。
		accepted := map[string]bool{}
		for _, a := range res.Accepted {
			accepted[a.ReservationID] = true
		}
		for _, r := range p.Reservations {
			if r.Confirmed && !accepted[r.ID] {
				t.Fatalf("trial %d: confirmed %s not accepted", trial, r.ID)
			}
		}
		for si, sched := range res.Schedule {
			for k := 1; k < len(sched.Entries); k++ {
				if sched.Entries[k-1].OccupyEnd > sched.Entries[k].OccupyStart {
					t.Fatalf("trial %d: overlap on spot %s", trial, p.Spots[si].ID)
				}
			}
		}
	}
}
