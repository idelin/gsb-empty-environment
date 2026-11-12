package queryid

import (
	"testing"
)

// 小全集：原子 a、b、c 的成员身份分别对应元素编号的第 0、1、2 位，
// 全集 {0..7} 恰好覆盖全部 2^3 种赋值，因此元素级枚举等价于
// 对基本集合的一切解释做穷举。
var enumAtoms = []string{"a", "b", "c"}

func concreteMask(t *testing.T, e *SetExpr) uint8 {
	t.Helper()
	var mask uint8
	for elem := 0; elem < 8; elem++ {
		v, err := e.Eval(func(name string) bool {
			for i, a := range enumAtoms {
				if name == a {
					return elem>>uint(i)&1 == 1
				}
			}
			return false
		})
		if err != nil {
			t.Fatalf("Eval(%v): %v", e, err)
		}
		if v {
			mask |= 1 << uint(elem)
		}
	}
	return mask
}

// allExprsUpToDepth 生成给定深度内的全部表达式。
func allExprsUpToDepth(depth int) []*SetExpr {
	leaves := []*SetExpr{Empty(), Full(), Base("a"), Base("b"), Base("c")}
	if depth == 0 {
		return leaves
	}
	prev := allExprsUpToDepth(depth - 1)
	out := append([]*SetExpr{}, prev...)
	for _, e := range prev {
		out = append(out, Comp(e))
	}
	for _, l := range prev {
		for _, r := range prev {
			out = append(out, Union(l, r), Inter(l, r), Diff(l, r))
		}
	}
	return out
}

// TestCanonMatchesSmallUniverseEnumeration 用小全集枚举验证：
// 规范化键相等 ⟺ 表达式在小全集上逐元素求值相等。
func TestCanonMatchesSmallUniverseEnumeration(t *testing.T) {
	exprs := allExprsUpToDepth(2)
	t.Logf("enumerating %d expressions over 8-element universe", len(exprs))
	keyToMask := map[string]uint8{}
	maskToKey := map[uint8]string{}
	for _, e := range exprs {
		c, err := e.Canonical()
		if err != nil {
			t.Fatalf("Canonical(%v): %v", e, err)
		}
		key := c.Key()
		mask := concreteMask(t, e)
		if m, ok := keyToMask[key]; ok && m != mask {
			t.Fatalf("key collision: %v and another expression share key %s but masks differ (%08b vs %08b)", e, key, m, mask)
		}
		if k, ok := maskToKey[mask]; ok && k != key {
			t.Fatalf("non-canonical: %v has key %s, but an equal-set expression has key %s", e, key, k)
		}
		keyToMask[key] = mask
		maskToKey[mask] = key
	}
	t.Logf("distinct keys: %d, distinct masks: %d", len(keyToMask), len(maskToKey))
	if len(keyToMask) != len(maskToKey) {
		t.Fatalf("key/mask count mismatch: %d keys vs %d masks", len(keyToMask), len(maskToKey))
	}
}

// TestEquivalentPairsShareKey 验证经典集合恒等式得到同一个键。
func TestEquivalentPairsShareKey(t *testing.T) {
	a, b, c := Base("a"), Base("b"), Base("c")
	pairs := []struct {
		name string
		x, y *SetExpr
	}{
		{"union comm", Union(a, b), Union(b, a)},
		{"inter comm", Inter(a, b), Inter(b, a)},
		{"union assoc", Union(Union(a, b), c), Union(a, Union(b, c))},
		{"inter assoc", Inter(Inter(a, b), c), Inter(a, Inter(b, c))},
		{"dist inter", Inter(a, Union(b, c)), Union(Inter(a, b), Inter(a, c))},
		{"dist union", Union(a, Inter(b, c)), Inter(Union(a, b), Union(a, c))},
		{"de morgan union", Comp(Union(a, b)), Inter(Comp(a), Comp(b))},
		{"de morgan inter", Comp(Inter(a, b)), Union(Comp(a), Comp(b))},
		{"absorb union", Union(a, Inter(a, b)), a},
		{"absorb inter", Inter(a, Union(a, b)), a},
		{"idempotent union", Union(a, a), a},
		{"idempotent inter", Inter(a, a), a},
		{"double comp", Comp(Comp(a)), a},
		{"diff as inter", Diff(a, b), Inter(a, Comp(b))},
		{"complement inter", Inter(a, Comp(a)), Empty()},
		{"complement union", Union(a, Comp(a)), Full()},
		{"union empty", Union(a, Empty()), a},
		{"inter full", Inter(a, Full()), a},
		{"union full", Union(a, Full()), Full()},
		{"inter empty", Inter(a, Empty()), Empty()},
		{"comp empty", Comp(Empty()), Full()},
		{"comp full", Comp(Full()), Empty()},
		{"diff self", Diff(a, a), Empty()},
	}
	for _, p := range pairs {
		kx, err := p.x.Canonical()
		if err != nil {
			t.Fatalf("%s: Canonical(%v): %v", p.name, p.x, err)
		}
		ky, err := p.y.Canonical()
		if err != nil {
			t.Fatalf("%s: Canonical(%v): %v", p.name, p.y, err)
		}
		if kx.Key() != ky.Key() {
			t.Errorf("%s: %v and %v should share key, got %s vs %s", p.name, p.x, p.y, kx.Key(), ky.Key())
		}
	}
}

// TestScopeExpansionChangesKey 验证扩大或改变分组边界必然换键，
// 这是防止复用已加噪结果造成差分泄漏的核心性质。
func TestScopeExpansionChangesKey(t *testing.T) {
	a, b, c := Base("a"), Base("b"), Base("c")
	base := Inter(a, b)
	baseKey, err := base.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	variants := map[string]*SetExpr{
		"widen union":     Union(Inter(a, b), c),
		"widen drop":      a,
		"narrow":          Inter(Inter(a, b), c),
		"subtract":        Diff(Inter(a, b), c),
		"swap atom":       Inter(a, c),
		"complement":      Comp(Inter(a, b)),
		"single widening": Union(a, b),
	}
	for name, v := range variants {
		kv, err := v.Canonical()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if kv.Key() == baseKey.Key() {
			t.Errorf("%s: %v must not share key with %v", name, v, base)
		}
	}
}

func mustIdentity(t *testing.T, domain *SetExpr, mech Mechanism) Identity {
	t.Helper()
	id, err := NewIdentity(domain, mech)
	if err != nil {
		t.Fatalf("NewIdentity: %v", err)
	}
	return id
}

func baseMechanism() Mechanism {
	return Mechanism{
		Agg:        "sum",
		Params:     []Rat{MustRat("1/2"), MustRat("3")},
		Adj:        AddRemove,
		Clip:       ClipRule{Kind: "l1-contrib", Bound: MustRat("4")},
		NoiseEpoch: 7,
	}
}

// TestMechanismSeparation 验证只有完全同机制才共享结果：
// 邻接、裁剪、噪声代次、参数、聚合种类、域任一不同即换键。
func TestMechanismSeparation(t *testing.T) {
	domain := Union(Base("a"), Base("b"))
	base := mustIdentity(t, domain, baseMechanism())

	same := mustIdentity(t,
		// 语法不同但语义相同的域表达式。
		Union(Base("b"), Union(Base("a"), Empty())),
		Mechanism{
			Agg:        "sum",
			Params:     []Rat{MustRat("2/4"), RatFromInt(3)}, // 2/4 == 1/2
			Adj:        AddRemove,
			Clip:       ClipRule{Kind: "l1-contrib", Bound: MustRat("8/2")}, // 8/2 == 4
			NoiseEpoch: 7,
		})
	if !base.SharesResultWith(same) {
		t.Errorf("identical mechanism and equivalent domain must share result:\n%s\n%s", base.Key(), same.Key())
	}

	mech := baseMechanism()
	variants := map[string]Identity{}

	m := mech
	m.Adj = Substitute
	variants["adjacency"] = mustIdentity(t, domain, m)

	m = mech
	m.Clip.Bound = MustRat("5")
	variants["clip bound"] = mustIdentity(t, domain, m)

	m = mech
	m.Clip.Kind = "value-cap"
	variants["clip kind"] = mustIdentity(t, domain, m)

	m = mech
	m.NoiseEpoch = 8
	variants["noise epoch"] = mustIdentity(t, domain, m)

	m = mech
	m.Params = []Rat{MustRat("1/2"), MustRat("4")}
	variants["param value"] = mustIdentity(t, domain, m)

	m = mech
	m.Params = []Rat{MustRat("1/2")}
	variants["param count"] = mustIdentity(t, domain, m)

	m = mech
	m.Agg = "count"
	variants["agg kind"] = mustIdentity(t, domain, m)

	variants["domain widened"] = mustIdentity(t, Union(domain, Base("c")), mech)

	seen := map[string]string{base.CacheKey(): "base"}
	for name, id := range variants {
		if base.SharesResultWith(id) {
			t.Errorf("%s: must not share result with base mechanism", name)
		}
		if prev, dup := seen[id.CacheKey()]; dup {
			t.Errorf("%s: cache key collides with %s", name, prev)
		}
		seen[id.CacheKey()] = name
	}
}

func TestParseSetExpr(t *testing.T) {
	a, b, c := Base("a"), Base("b"), Base("c")
	cases := []struct {
		src  string
		want *SetExpr
	}{
		{"a", a},
		{"a|b&c", Union(a, Inter(b, c))}, // 交优先于并
		{"a-b-c", Diff(Diff(a, b), c)},   // 差集左结合
		{"!a&b", Inter(Comp(a), b)},      // 补优先于交
		{"!(a|b)", Comp(Union(a, b))},
		{"(a|b)&(c-a)", Inter(Union(a, b), Diff(c, a))},
		{"a ∪ b ∩ c", Union(a, Inter(b, c))},
		{"¬(a ∖ b)", Comp(Diff(a, b))},
		{"∅", Empty()},
		{"EMPTY|a", Union(Empty(), a)},
		{"FULL&a", Inter(Full(), a)},
		{"_x9|B2", Union(Base("_x9"), Base("B2"))},
	}
	for _, tc := range cases {
		got, err := ParseSetExpr(tc.src)
		if err != nil {
			t.Errorf("ParseSetExpr(%q): %v", tc.src, err)
			continue
		}
		gk, _ := got.Canonical()
		wk, _ := tc.want.Canonical()
		if gk.Key() != wk.Key() {
			t.Errorf("ParseSetExpr(%q) = %v, want %v", tc.src, got, tc.want)
		}
	}

	// 解析出的德摩根等式两边键相同。
	lhs, err := ParseSetExpr("!(a|b)")
	if err != nil {
		t.Fatal(err)
	}
	rhs, err := ParseSetExpr("!a&!b")
	if err != nil {
		t.Fatal(err)
	}
	lk, _ := lhs.Canonical()
	rk, _ := rhs.Canonical()
	if lk.Key() != rk.Key() {
		t.Errorf("parsed De Morgan pair should share key: %s vs %s", lk.Key(), rk.Key())
	}

	for _, bad := range []string{"", "a|", "(a", "a)", "a b", "1a", "&a", "a&&b"} {
		if _, err := ParseSetExpr(bad); err == nil {
			t.Errorf("ParseSetExpr(%q) should fail", bad)
		}
	}
}

func TestRatNormalization(t *testing.T) {
	eq := [][]string{
		{"1/2", "2/4", "3/6", "0.5", "-1/-2"},
		{"0", "0/7", "-0/3"},
		{"-3/4", "6/-8", "-0.75"},
		{"2", "4/2", "2.0"},
	}
	for _, group := range eq {
		var key string
		for i, s := range group {
			r, err := ParseRat(s)
			if err != nil {
				t.Fatalf("ParseRat(%q): %v", s, err)
			}
			if i == 0 {
				key = r.Key()
				continue
			}
			if r.Key() != key {
				t.Errorf("ParseRat(%q).Key() = %s, want %s", s, r.Key(), key)
			}
		}
	}
	if NewRat(1, 2).Equal(NewRat(2, 3)) {
		t.Errorf("1/2 should not equal 2/3")
	}
	if _, err := ParseRat("1/0"); err == nil {
		t.Errorf("ParseRat(1/0) should fail")
	}
	if _, err := ParseRat("abc"); err == nil {
		t.Errorf("ParseRat(abc) should fail")
	}
}

func TestTooManyAtoms(t *testing.T) {
	e := Base("x00")
	for i := 1; i <= MaxAtoms; i++ {
		e = Union(e, Base("x"+string(rune('a'+i))+string(rune('0'+i%10))))
	}
	if _, err := e.Canonical(); err == nil {
		t.Fatalf("expression with %d atoms should exceed MaxAtoms=%d", MaxAtoms+1, MaxAtoms)
	}
}
