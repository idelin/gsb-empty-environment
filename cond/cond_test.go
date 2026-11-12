package cond

import (
	"strings"
	"testing"
)

func TestLeafSemantics(t *testing.T) {
	cases := []struct {
		name string
		expr Expr
		in   Labels
		want Tri
	}{
		{"exists 缺失", Exists{"team"}, Labels{}, False},
		{"exists 空集合", Exists{"team"}, Labels{"team": Set()}, True},
		{"exists 非空", Exists{"team"}, Labels{"team": Set("sec")}, True},
		{"anyEq 缺失", AnyEq{"role", "admin"}, Labels{}, Unknown},
		{"anyEq 空集合", AnyEq{"role", "admin"}, Labels{"role": Set()}, False},
		{"anyEq 命中", AnyEq{"role", "admin"}, Labels{"role": Set("dev", "admin")}, True},
		{"anyEq 未命中", AnyEq{"role", "admin"}, Labels{"role": Set("dev")}, False},
		{"allEq 缺失", AllEq{"scope", "read"}, Labels{}, Unknown},
		{"allEq 空集合空真", AllEq{"scope", "read"}, Labels{"scope": Set()}, True},
		{"allEq 全体相等", AllEq{"scope", "read"}, Labels{"scope": Set("read")}, True},
		{"allEq 存在反例", AllEq{"scope", "read"}, Labels{"scope": Set("read", "write")}, False},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, _ := Eval(c.expr, c.in)
			if got != c.want {
				t.Fatalf("Eval(%s, %v) = %s, want %s", c.expr, c.in, got, c.want)
			}
		})
	}
}

func TestMissingIsNotEmptyString(t *testing.T) {
	// 回归：缺失标签不得被当作空字符串而误放行。
	expr := Neg{AnyEq{"role", "blocked"}}
	got, _ := Eval(expr, Labels{})
	if got != Unknown {
		t.Fatalf("not(anyEq) 对缺失标签 = %s, want unknown", got)
	}
	if Allow(got) {
		t.Fatal("缺失标签不得放行")
	}
}

func TestOnlyTrueAllows(t *testing.T) {
	for _, tri := range []Tri{True, False, Unknown} {
		if Allow(tri) != (tri == True) {
			t.Fatalf("Allow(%s) 判定错误", tri)
		}
	}
}

func TestShortCircuitEvidence(t *testing.T) {
	expr := Conj{Args: []Expr{
		Exists{"team"},
		AnyEq{"role", "admin"},
		AllEq{"scope", "read"},
	}}
	// team 缺失 => 第一子句为假，后两个子句必须被短路跳过。
	r, ev := Eval(expr, Labels{})
	if r != False {
		t.Fatalf("结果 = %s, want false", r)
	}
	if len(ev.Steps) != 4 {
		t.Fatalf("证据步数 = %d, want 4（3 子句 + 1 根）", len(ev.Steps))
	}
	for i, wantSkipped := range []bool{false, true, true, false} {
		if ev.Steps[i].Skipped != wantSkipped {
			t.Errorf("步骤 %d (%s) Skipped = %v, want %v",
				i, ev.Steps[i], ev.Steps[i].Skipped, wantSkipped)
		}
	}
	// 析取在真处短路。
	or := Disj{Args: []Expr{Exists{"a"}, Exists{"b"}}}
	r, ev = Eval(or, Labels{"a": Set("x")})
	if r != True || !ev.Steps[1].Skipped {
		t.Fatalf("or 短路证据错误: r=%s steps=%v", r, ev.Steps)
	}
	// 未知不触发短路，后续子句仍被求值。
	r, ev = Eval(expr, Labels{"team": Set("sec")})
	if r != Unknown {
		t.Fatalf("role 缺失时结果 = %s, want unknown", r)
	}
	for i := 0; i < 3; i++ {
		if ev.Steps[i].Skipped {
			t.Errorf("未知不得短路，步骤 %d 被错误跳过", i)
		}
	}
}

func TestCompile(t *testing.T) {
	spec := map[string]any{
		"op": "and",
		"args": []any{
			map[string]any{"op": "exists", "label": "team"},
			map[string]any{"op": "not", "arg": map[string]any{
				"op": "anyEq", "label": "role", "value": "blocked",
			}},
		},
	}
	expr, err := Compile(spec)
	if err != nil {
		t.Fatal(err)
	}
	allow, tri, _ := Decide(expr, Labels{"team": Set("sec"), "role": Set("dev")})
	if !allow || tri != True {
		t.Fatalf("正常请求应放行, allow=%v tri=%s", allow, tri)
	}
	allow, tri, _ = Decide(expr, Labels{"role": Set("dev")})
	if allow || tri != False {
		t.Fatalf("team 缺失应拒绝(false), allow=%v tri=%s", allow, tri)
	}
	allow, tri, _ = Decide(expr, Labels{"team": Set("sec")})
	if allow || tri != Unknown {
		t.Fatalf("role 缺失应拒绝(unknown), allow=%v tri=%s", allow, tri)
	}
	if _, err := Compile(map[string]any{"op": "bogus"}); err == nil {
		t.Fatal("未知操作应报错")
	}
}

// TestSimplifyPreservesTri 用有限集合穷举验证化简保持三态结果。
func TestSimplifyPreservesTri(t *testing.T) {
	universe := []string{"x", "y"}
	exprs := []Expr{
		Neg{Neg{AnyEq{"a", "x"}}},
		Neg{Conj{Args: []Expr{AnyEq{"a", "x"}, AllEq{"b", "y"}}}},
		Neg{Disj{Args: []Expr{Exists{"a"}, AnyEq{"b", "x"}}}},
		Conj{Args: []Expr{
			Conj{Args: []Expr{AnyEq{"a", "x"}, AnyEq{"a", "x"}}},
			Neg{Neg{AllEq{"b", "y"}}},
		}},
		Neg{Neg{Neg{Disj{Args: []Expr{
			AllEq{"a", "x"},
			Neg{Conj{Args: []Expr{Exists{"b"}, AnyEq{"a", "y"}}}},
		}}}}},
	}
	for _, e := range exprs {
		if bad := CheckSimplify(e, universe); len(bad) > 0 {
			t.Errorf("化简改变三态结果: %s -> %s\n首个反例: %s",
				e, Simplify(e), bad[0])
		}
	}
}

// TestSimplifyActuallySimplifies 确认化简确实生效而非恒等。
func TestSimplifyActuallySimplifies(t *testing.T) {
	e := Neg{Neg{Conj{Args: []Expr{
		Conj{Args: []Expr{AnyEq{"a", "x"}, AnyEq{"a", "x"}}},
		Exists{"b"},
	}}}}
	got := Simplify(e).String()
	want := "and(anyEq(a,\"x\") exists(b))"
	if got != want {
		t.Fatalf("Simplify = %s, want %s", got, want)
	}
}

// TestExhaustiveSanity 穷举空间规模与三态真值表抽查。
func TestExhaustiveSanity(t *testing.T) {
	got := len(Enumerate([]string{"a", "b"}, []string{"x", "y"}))
	if want := 5 * 5; got != want {
		t.Fatalf("穷举组合数 = %d, want %d", got, want)
	}
	// De Morgan 在三态下成立：¬(a∧b) ≡ ¬a∨¬b。
	a, b := AnyEq{"a", "x"}, AllEq{"b", "y"}
	lhs := Neg{Conj{Args: []Expr{a, b}}}
	rhs := Disj{Args: []Expr{Neg{a}, Neg{b}}}
	if bad := CheckEquivalent(lhs, rhs, []string{"x", "y"}); len(bad) > 0 {
		t.Fatalf("De Morgan 在三态下不成立: %s", bad[0])
	}
}

func TestEvidenceString(t *testing.T) {
	expr := Disj{Args: []Expr{Exists{"a"}, Exists{"b"}}}
	_, ev := Eval(expr, Labels{"a": Set()})
	text := ev.Steps[1].String()
	if !strings.Contains(text, "skipped") {
		t.Fatalf("跳过证据缺少 skipped 标记: %q", text)
	}
}
