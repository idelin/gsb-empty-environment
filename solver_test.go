package main

import "testing"

// bruteForce 暴力枚举所有完整赋值，返回满足硬约束的最小软惩罚。
// 用于对拍验证分支定界的最优性。
func bruteForce(p *Problem) (int, bool) {
	n := len(p.VarNames)
	a := make(Assignment, n)
	best := -1
	var rec func(depth int)
	rec = func(depth int) {
		if depth == n {
			for _, h := range p.Hard {
				if h.Eval(a) == Violated {
					return
				}
			}
			penalty := 0
			for _, s := range p.Soft {
				if s.Eval(a) == Violated {
					penalty += s.Weight
				}
			}
			if best == -1 || penalty < best {
				best = penalty
			}
			return
		}
		for _, v := range p.Domains[depth] {
			a[depth] = v
			rec(depth + 1)
		}
	}
	rec(0)
	if best == -1 {
		return 0, false
	}
	return best, true
}

// TestSolveMatchesBruteForce 在一个小型合成问题上对拍暴力枚举。
func TestSolveMatchesBruteForce(t *testing.T) {
	// 3 个变量，域 {0,1,2}；硬约束：全部不同；
	// 软约束：三者都希望自己取 0，权重 5/2/1。
	// 全不同迫使至多一个偏好被满足，最优是保住 w5，惩罚 = 2+1 = 3。
	p := &Problem{
		VarNames: []string{"v0", "v1", "v2"},
		Domains:  [][]int{{0, 1, 2}, {0, 1, 2}, {0, 1, 2}},
		Hard: []HardConstraint{
			{Name: "all-different", Eval: func(a Assignment) Status {
				seen := map[int]bool{}
				for _, v := range a {
					if v == -1 {
						continue
					}
					if seen[v] {
						return Violated
					}
					seen[v] = true
				}
				return Satisfied
			}},
		},
		Soft: []SoftConstraint{
			{Name: "v0==0", Weight: 5, Eval: func(a Assignment) Status {
				if a[0] == -1 {
					return Undecided
				}
				if a[0] == 0 {
					return Satisfied
				}
				return Violated
			}},
			{Name: "v1==0", Weight: 2, Eval: func(a Assignment) Status {
				if a[1] == -1 {
					return Undecided
				}
				if a[1] == 0 {
					return Satisfied
				}
				return Violated
			}},
			{Name: "v2==0", Weight: 1, Eval: func(a Assignment) Status {
				if a[2] == -1 {
					return Undecided
				}
				if a[2] == 0 {
					return Satisfied
				}
				return Violated
			}},
		},
	}

	sol := Solve(p)
	if !sol.Feasible {
		t.Fatal("expected a feasible solution")
	}
	want, ok := bruteForce(p)
	if !ok {
		t.Fatal("brute force found no feasible solution")
	}
	if sol.Penalty != want {
		t.Fatalf("penalty = %d, want brute-force optimum %d", sol.Penalty, want)
	}
	if sol.Penalty != 3 {
		t.Fatalf("penalty = %d, want 3", sol.Penalty)
	}
}

// TestSolveInfeasible 硬约束互相矛盾时应报告无可行解。
func TestSolveInfeasible(t *testing.T) {
	p := &Problem{
		VarNames: []string{"x"},
		Domains:  [][]int{{0}},
		Hard: []HardConstraint{
			{Name: "x!=0", Eval: func(a Assignment) Status {
				if a[0] == 0 {
					return Violated
				}
				return Satisfied
			}},
		},
	}
	if sol := Solve(p); sol.Feasible {
		t.Fatalf("expected infeasible, got %+v", sol)
	}
}
