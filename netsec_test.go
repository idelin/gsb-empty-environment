package netsec_test

import (
	"slices"
	"testing"

	"example.org/netsec/q0380"
)

func actionRank(action netsec.Action) int {
	switch action {
	case netsec.Kill:
		return 3
	case netsec.Deny:
		return 2
	case netsec.Notify:
		return 1
	default:
		return 0
	}
}

func mustKey(t *testing.T, syscall uint, args ...uint64) netsec.Key {
	t.Helper()
	var arr [2]uint64
	copy(arr[:], args)
	mask := uint8(0)
	for i := range args {
		mask |= 1 << i
	}
	key, err := netsec.NewKey(syscall, mask, arr)
	if err != nil {
		t.Fatalf("NewKey: %v", err)
	}
	return key
}

func mustTable(t *testing.T, name string, rules map[netsec.Key]netsec.Decision, def netsec.Decision) *netsec.Table {
	t.Helper()
	table, err := netsec.NewTable(name, rules, def)
	if err != nil {
		t.Fatalf("NewTable(%s): %v", name, err)
	}
	return table
}

func testDomain(t *testing.T) *netsec.Domain {
	t.Helper()
	domain, err := netsec.NewDomain(
		[]uint{1, 2, 3},
		[2][]uint64{{0, 1}, {10, 20}},
	)
	if err != nil {
		t.Fatalf("NewDomain: %v", err)
	}
	return domain
}

// 显式场景：后安装的允许不得覆盖早期拒绝；更高严格度动作优先。
func TestActionPriorityOrder(t *testing.T) {
	request := mustKey(t, 42, 7, 9)

	denyFirst := mustTable(t, "early-deny", map[netsec.Key]netsec.Decision{
		request: {Action: netsec.Deny, Errno: 1, Tag: "deny"},
	}, netsec.Decision{Action: netsec.Allow})
	allowLater := mustTable(t, "late-allow", map[netsec.Key]netsec.Decision{
		request: {Action: netsec.Allow, Tag: "allow"},
	}, netsec.Decision{Action: netsec.Allow})
	killLatest := mustTable(t, "latest-kill", map[netsec.Key]netsec.Decision{
		request: {Action: netsec.Kill, Signal: 9, Tag: "kill"},
	}, netsec.Decision{Action: netsec.Allow})

	chain, err := netsec.NewChain().Append(denyFirst)
	if err != nil {
		t.Fatal(err)
	}
	if decision, index := chain.Resolve(request); decision.Action != netsec.Deny || index != 0 {
		t.Fatalf("early deny: got %s index %d", decision.Action, index)
	}

	chain, err = chain.Append(allowLater)
	if err != nil {
		t.Fatal(err)
	}
	if decision, _ := chain.Resolve(request); decision.Action != netsec.Deny || decision.Tag != "deny" {
		t.Fatalf("late allow must not override early deny, got %s tag %q", decision.Action, decision.Tag)
	}

	chain, err = chain.Append(killLatest)
	if err != nil {
		t.Fatal(err)
	}
	if decision, index := chain.Resolve(request); decision.Action != netsec.Kill || decision.Signal != 9 || index != 2 {
		t.Fatalf("kill must outrank deny, got %s index %d", decision.Action, index)
	}

	empty := netsec.NewChain()
	if decision, index := empty.Resolve(request); decision.Action != netsec.Allow || index != -1 {
		t.Fatalf("empty chain baseline: got %s index %d", decision.Action, index)
	}
}

// 同严格度时取最近安装者：穷举四张不同动作表的全部 4! 种安装顺序与全部请求键。
func TestPriorityAndTieBreakExhaustive(t *testing.T) {
	domain := testDomain(t)
	actions := []netsec.Action{netsec.Allow, netsec.Notify, netsec.Deny, netsec.Kill}

	decisions := make([]netsec.Decision, len(actions))
	for i, action := range actions {
		decisions[i] = netsec.Decision{
			Action: action,
			Errno:  uint16(i + 1),
			Signal: uint16(i + 1),
			Tag:    action.String(),
		}
	}

	for _, request := range domain.Enumerate() {
		tables := make([]*netsec.Table, len(actions))
		for i, decision := range decisions {
			tables[i] = mustTable(t, "table-"+decision.Action.String(),
				map[netsec.Key]netsec.Decision{request: decision},
				netsec.Decision{Action: netsec.Allow})
		}
		for _, order := range permutations([]int{0, 1, 2, 3}) {
			chain := netsec.NewChain()
			for _, pos := range order {
				next, err := chain.Append(tables[pos])
				if err != nil {
					t.Fatal(err)
				}
				chain = next
			}
			got, index := chain.Resolve(request)

			wantMax := order[0]
			for _, pos := range order {
				if actionRank(actions[pos]) > actionRank(actions[wantMax]) {
					wantMax = pos
				}
			}
			if got.Action != actions[wantMax] {
				t.Fatalf("request %+v order %v: want %s got %s", request, order, actions[wantMax], got.Action)
			}

			wantIndex := -1
			for asIndex, pos := range order {
				if pos == wantMax {
					wantIndex = asIndex
				}
			}
			if index != wantIndex || got.Tag != actions[wantMax].String() {
				t.Fatalf("request %+v order %v: want index %d tag %q, got index %d tag %q",
					request, order, wantIndex, actions[wantMax].String(), index, got.Tag)
			}
		}
	}
}

// 两张同动作表冲突时，最近安装者的载荷（Errno/Tag）必须生效。
func TestEqualPriorityLatestDataWins(t *testing.T) {
	request := mustKey(t, 5, 1)
	early := mustTable(t, "early", map[netsec.Key]netsec.Decision{
		request: {Action: netsec.Deny, Errno: 1, Tag: "early"},
	}, netsec.Decision{Action: netsec.Allow})
	later := mustTable(t, "later", map[netsec.Key]netsec.Decision{
		request: {Action: netsec.Deny, Errno: 42, Tag: "later"},
	}, netsec.Decision{Action: netsec.Allow})

	chain, err := netsec.NewChain().Append(early)
	if err != nil {
		t.Fatal(err)
	}
	chain, err = chain.Append(later)
	if err != nil {
		t.Fatal(err)
	}
	decision, index := chain.Resolve(request)
	if decision.Action != netsec.Deny || decision.Errno != 42 || decision.Tag != "later" || index != 1 {
		t.Fatalf("equal priority latest data: got %+v index %d", decision, index)
	}
}

func TestDomainEnumerationIsFiniteAndStable(t *testing.T) {
	domain := testDomain(t)
	first := domain.Enumerate()
	if len(first) != domain.Size() || domain.Size() != 3*2*2 {
		t.Fatalf("domain size: got %d want 12", domain.Size())
	}
	second := domain.Enumerate()
	if !slices.EqualFunc(first, second, func(a, b netsec.Key) bool { return a == b }) {
		t.Fatal("enumeration order must be deterministic")
	}
	seen := make(map[netsec.Key]struct{}, len(first))
	for _, key := range first {
		if _, dup := seen[key]; dup {
			t.Fatalf("duplicate key %+v", key)
		}
		seen[key] = struct{}{}
	}
}

func permutations(values []int) [][]int {
	var result [][]int
	var generate func([]int, int)
	generate = func(current []int, start int) {
		if start == len(current) {
			result = append(result, append([]int(nil), current...))
			return
		}
		for i := start; i < len(current); i++ {
			current[start], current[i] = current[i], current[start]
			generate(current, start+1)
			current[start], current[i] = current[i], current[start]
		}
	}
	generate(append([]int(nil), values...), 0)
	return result
}
