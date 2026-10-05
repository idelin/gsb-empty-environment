package main

import (
	"math/rand"
	"testing"
)

func gs(users ...int64) []Group {
	out := make([]Group, len(users))
	for i, u := range users {
		out[i] = Group{Name: string(rune('a' + i)), Users: u, Version: "stable"}
	}
	return out
}

// 可行场景：存在精确解，且迁移代价最小。
func TestSolveFeasibleMinimal(t *testing.T) {
	groups := gs(10, 20, 30, 40)
	// 当前全部在 stable，目标 canary，区间 [50,50]。
	// 可行子集：{20,30}、{10,40}，迁移代价均为 50。
	sol, _, err := Solve(groups, "canary", Interval{50, 50})
	if err != nil {
		t.Fatal(err)
	}
	if sol == nil {
		t.Fatal("expected feasible")
	}
	if sol.NewUsers != 50 || sol.MovedUsers != 50 {
		t.Fatalf("got %+v", sol)
	}
}

// 最小迁移：已有组在目标版本上时，不动它们应更优。
func TestSolvePrefersNotMoving(t *testing.T) {
	groups := gs(10, 20, 30, 40)
	groups[3].Version = "canary" // 40 人的组已在目标版本
	// 区间 [40,40]：方案 A 不动任何组（40 已在）；方案 B 迁移 {10,30} 并回退 40，代价 80。
	sol, _, err := Solve(groups, "canary", Interval{40, 40})
	if err != nil {
		t.Fatal(err)
	}
	if sol == nil {
		t.Fatal("expected feasible")
	}
	if sol.MovedUsers != 0 || sol.NewUsers != 40 {
		t.Fatalf("got %+v", sol)
	}
}

// 不可达：所有组人数为偶数，奇数区间不可达，gcd 判据成立。
func TestInfeasibleGCD(t *testing.T) {
	groups := gs(2, 4, 6, 8)
	sol, reachable, err := Solve(groups, "canary", Interval{7, 7})
	if err != nil {
		t.Fatal(err)
	}
	if sol != nil {
		t.Fatal("expected infeasible")
	}
	p := BuildProof(groups, Interval{7, 7}, reachable)
	if !p.ArithProof {
		t.Fatal("expected arithmetic proof")
	}
	if p.Predecessor != 6 || p.Successor != 8 {
		t.Fatalf("pred/succ = %d/%d", p.Predecessor, p.Successor)
	}
	if err := VerifyProof(groups, p); err != nil {
		t.Fatal("proof should verify:", err)
	}
}

// 不可达但 gcd=1：只能靠完备枚举证明。
func TestInfeasibleEnumeration(t *testing.T) {
	groups := gs(3, 5, 11) // 可达: 0,3,5,8,11,14,16,19
	sol, reachable, err := Solve(groups, "canary", Interval{9, 10})
	if err != nil {
		t.Fatal(err)
	}
	if sol != nil {
		t.Fatal("expected infeasible")
	}
	p := BuildProof(groups, Interval{9, 10}, reachable)
	if p.ArithProof {
		t.Fatal("gcd=1, should not have arithmetic proof")
	}
	if p.Predecessor != 8 || p.Successor != 11 {
		t.Fatalf("pred/succ = %d/%d", p.Predecessor, p.Successor)
	}
	if err := VerifyProof(groups, p); err != nil {
		t.Fatal("proof should verify:", err)
	}
}

// 篡改证明必须被拒绝。
func TestVerifyRejectsTampered(t *testing.T) {
	groups := gs(3, 5, 11)
	_, reachable, _ := Solve(groups, "canary", Interval{9, 10})
	p := BuildProof(groups, Interval{9, 10}, reachable)
	p.ReachableSums = append(p.ReachableSums[:3], p.ReachableSums[4:]...) // 删掉一个可达值
	if err := VerifyProof(groups, p); err == nil {
		t.Fatal("tampered proof must be rejected")
	}
}

// 区间端点取整：比例区间与人数区间严格对应。
func TestIntervalFromRatio(t *testing.T) {
	iv := IntervalFromRatio(1000, 0.30, 0.005)
	if iv.Lo != 295 || iv.Hi != 305 {
		t.Fatalf("got [%d,%d]", iv.Lo, iv.Hi)
	}
	iv = IntervalFromRatio(7, 0.5, 0)
	if iv.Lo != 4 || iv.Hi != 3 { // 0.5*7=3.5，无整数解 → 空区间
		t.Fatalf("got [%d,%d]", iv.Lo, iv.Hi)
	}
}

// 20 组上限：随机实例与暴力对拍，验证最优性与证明正确性。
func TestTwentyGroupsBruteForce(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	for trial := 0; trial < 50; trial++ {
		n := 1 + r.Intn(MaxGroups)
		groups := make([]Group, n)
		for i := range groups {
			groups[i] = Group{
				Name:    string(rune('a'+i%26)) + string(rune('0'+i/26)),
				Users:   int64(1 + r.Intn(100)),
				Version: "stable",
			}
			if r.Intn(2) == 0 {
				groups[i].Version = "canary"
			}
		}
		total := TotalUsers(groups)
		lo := int64(r.Intn(int(total) + 1))
		hi := lo + int64(r.Intn(int(total-lo)+1))
		iv := Interval{lo, hi}

		sol, reachable, err := Solve(groups, "canary", iv)
		if err != nil {
			t.Fatal(err)
		}
		// 暴力校验
		bestCost := int64(-1)
		size := 1 << uint(n)
		for mask := 0; mask < size; mask++ {
			var sum, cost int64
			for i := 0; i < n; i++ {
				on := mask&(1<<uint(i)) != 0
				cur := groups[i].Version == "canary"
				if on {
					sum += groups[i].Users
				}
				if on != cur {
					cost += groups[i].Users
				}
			}
			if sum >= lo && sum <= hi && (bestCost < 0 || cost < bestCost) {
				bestCost = cost
			}
		}
		if bestCost < 0 {
			if sol != nil {
				t.Fatalf("trial %d: solver found solution but brute force says infeasible", trial)
			}
			p := BuildProof(groups, iv, reachable)
			if err := VerifyProof(groups, p); err != nil {
				t.Fatalf("trial %d: proof invalid: %v", trial, err)
			}
		} else {
			if sol == nil {
				t.Fatalf("trial %d: solver says infeasible but brute force found cost %d", trial, bestCost)
			}
			if sol.MovedUsers != bestCost {
				t.Fatalf("trial %d: solver cost %d, brute force %d", trial, sol.MovedUsers, bestCost)
			}
			if sol.NewUsers < lo || sol.NewUsers > hi {
				t.Fatalf("trial %d: solution out of interval", trial)
			}
		}
	}
}

// 组不可分割：解的每个组要么整体在目标版本、要么整体不在（由掩码表示保证），
// 这里验证输出人数恰好是若干完整组人数之和。
func TestAtomicity(t *testing.T) {
	groups := gs(7, 13, 29)
	sol, _, err := Solve(groups, "canary", Interval{20, 40})
	if err != nil {
		t.Fatal(err)
	}
	if sol == nil {
		t.Fatal("expected feasible")
	}
	var sum int64
	for i, g := range groups {
		if sol.Mask&(1<<uint(i)) != 0 {
			sum += g.Users
		}
	}
	if sum != sol.NewUsers {
		t.Fatalf("mask inconsistent with reported users")
	}
}
