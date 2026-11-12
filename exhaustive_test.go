package taskpkg

import (
	"math/rand"
	"testing"
)

// bruteForceVerdict 是独立于 Analyze 实现的穷举预言机：枚举全部合法
// 拓扑序，在每个全序上直接检查是否存在完整三段证据，再按“所有/部分/
// 无”拓扑序支持攻击得出必然/可能/不成立。
func bruteForceVerdict(t *testing.T, in Input) Verdict {
	t.Helper()
	orders, truncated, err := LinearExtensions(in, 0)
	if err != nil {
		t.Fatalf("LinearExtensions error: %v", err)
	}
	if truncated {
		t.Fatal("default enumeration limit reached unexpectedly")
	}
	subjects, err := buildLineage(in.SubjectDerives)
	if err != nil {
		t.Fatalf("subject lineage error: %v", err)
	}
	devices, err := buildLineage(in.DeviceDerives)
	if err != nil {
		t.Fatalf("device lineage error: %v", err)
	}

	byID := make(map[string]Event, len(in.Events))
	for _, e := range in.Events {
		byID[e.ID] = e
	}
	revByID := make(map[string]Revocation, len(in.Revocations))
	for _, r := range in.Revocations {
		revByID[r.ID] = r
	}

	var reads, auths, ops []Event
	for _, e := range in.Events {
		switch e.Kind {
		case KindTokenRead:
			reads = append(reads, e)
		case KindTokenAuth:
			auths = append(auths, e)
		case KindProtectedOp:
			ops = append(ops, e)
		}
	}

	supported := 0
	for _, order := range orders {
		pos := make(map[string]int, len(order))
		for i, id := range order {
			pos[id] = i
		}
		hit := false
		for _, r := range reads {
			for _, a := range auths {
				if !sameTokenFlow(r, a, subjects, devices) {
					continue
				}
				for _, o := range ops {
					if !boundToAuth(a, o) {
						continue
					}
					for _, rev := range in.Revocations {
						if rev.Token != r.Token {
							continue
						}
						if pos[r.ID] < pos[a.ID] &&
							pos[a.ID] < pos[o.ID] &&
							pos[rev.ID] < pos[o.ID] {
							hit = true
						}
					}
				}
			}
		}
		if hit {
			supported++
		}
	}
	switch supported {
	case 0:
		return VerdictNotEstablished
	case len(orders):
		return VerdictCertain
	default:
		return VerdictPossible
	}
}

// 对全部表驱动场景做穷举对照，确保 Analyze 与全拓扑序预言机一致。
func TestAnalyzeMatchesExhaustiveOracle(t *testing.T) {
	tok := token("iss", "digest", 1)
	u1, u2, u3 := inst("svc", 1), inst("svc", 2), inst("svc", 3)
	d1, d2, d3 := inst("host", 1), inst("host", 2), inst("host", 3)

	cases := []Input{
		{
			Events: []Event{
				ev("r", KindTokenRead, tok, u1, d1, ""),
				ev("a", KindTokenAuth, tok, u2, d2, "s1", "r"),
				ev("o", KindProtectedOp, tok, u2, d2, "s1", "a", "v"),
			},
			Revocations:    []Revocation{{ID: "v", Token: tok, After: []string{"a"}}},
			SubjectDerives: []DeriveEdge{{From: u1, To: u2}},
			DeviceDerives:  []DeriveEdge{{From: d1, To: d2}},
		},
		{
			Events: []Event{
				ev("r", KindTokenRead, tok, u1, d1, ""),
				ev("a", KindTokenAuth, tok, u2, d2, "s1"),
				ev("o", KindProtectedOp, tok, u2, d2, "s1"),
			},
			Revocations:    []Revocation{{ID: "v", Token: tok}},
			SubjectDerives: []DeriveEdge{{From: u1, To: u2}},
			DeviceDerives:  []DeriveEdge{{From: d1, To: d2}},
		},
		{
			Events: []Event{
				ev("r", KindTokenRead, tok, u1, d1, ""),
				ev("a", KindTokenAuth, tok, u2, d2, "s1", "r"),
				ev("o", KindProtectedOp, tok, u2, d2, "s1", "a"),
			},
			Revocations:    []Revocation{{ID: "v", Token: tok, After: []string{"o"}}},
			SubjectDerives: []DeriveEdge{{From: u1, To: u2}},
			DeviceDerives:  []DeriveEdge{{From: d1, To: d2}},
		},
		{
			Events: []Event{
				ev("r", KindTokenRead, tok, u1, d1, ""),
				ev("a", KindTokenAuth, tok, u2, d3, "s1", "r"),
				ev("o", KindProtectedOp, tok, u2, d3, "s1", "a"),
			},
			Revocations:    []Revocation{{ID: "v", Token: tok}},
			SubjectDerives: []DeriveEdge{{From: u1, To: u2}},
			DeviceDerives:  []DeriveEdge{{From: d1, To: d2}, {From: d2, To: d3}},
		},
		{
			Events: []Event{
				ev("r", KindTokenRead, tok, u1, d1, ""),
				ev("a", KindTokenAuth, tok, u3, d3, "s1", "r"),
				ev("o", KindProtectedOp, tok, u3, d3, "s1", "a"),
				ev("x", KindTokenRead, tok, u2, d2, ""),
			},
			Revocations: []Revocation{{ID: "v", Token: tok}},
			SubjectDerives: []DeriveEdge{
				{From: u1, To: u2}, {From: u2, To: u3},
			},
			DeviceDerives: []DeriveEdge{
				{From: d1, To: d2}, {From: d2, To: d3},
			},
		},
	}

	for i, in := range cases {
		report, err := Analyze(in)
		if err != nil {
			t.Fatalf("case %d: Analyze error: %v", i, err)
		}
		want := bruteForceVerdict(t, in)
		if report.Verdict != want {
			t.Fatalf("case %d: Analyze verdict %s, exhaustive oracle %s",
				i, report.Verdict, want)
		}
	}
}

// TestRandomInputsMatchOracle 随机生成大量带偏序的合法输入，逐一枚举
// 全部拓扑序与 Analyze 比对，覆盖表测试之外的组合。
func TestRandomInputsMatchOracle(t *testing.T) {
	rng := rand.New(rand.NewSource(20261112))
	subjects := []Instance{inst("svc", 1), inst("svc", 2), inst("svc", 3), inst("svc", 4)}
	devices := []Instance{inst("host", 1), inst("host", 2), inst("host", 3), inst("host", 4)}
	tokens := []TokenID{token("iss", "d1", 1), token("iss", "d2", 1), token("iss", "d1", 2)}

	for iter := 0; iter < 250; iter++ {
		n := 3 + rng.Intn(7) // 3..9 个事件，保证三类事件都存在
		kinds := []Kind{KindTokenRead, KindTokenAuth, KindProtectedOp}
		for len(kinds) < n {
			kinds = append(kinds, []Kind{KindTokenRead, KindTokenAuth, KindProtectedOp}[rng.Intn(3)])
		}
		rng.Shuffle(len(kinds), func(i, j int) { kinds[i], kinds[j] = kinds[j], kinds[i] })

		events := make([]Event, n)
		for i := 0; i < n; i++ {
			k := kinds[i]
			session := ""
			if k != KindTokenRead {
				session = "sess" + string(rune('0'+rng.Intn(3)))
			}
			events[i] = Event{
				ID:      "e" + string(rune('a'+i)),
				Kind:    k,
				Token:   tokens[rng.Intn(len(tokens))],
				Subject: subjects[rng.Intn(len(subjects))],
				Device:  devices[rng.Intn(len(devices))],
				Session: session,
			}
			// 只让更早的事件成为前驱，保证偏序无环。
			for j := 0; j < i; j++ {
				if rng.Intn(2) == 0 {
					events[i].After = append(events[i].After, events[j].ID)
				}
			}
		}

		var subjectEdges, deviceEdges []DeriveEdge
		for i := 1; i < len(subjects); i++ {
			if rng.Intn(2) == 0 {
				subjectEdges = append(subjectEdges, DeriveEdge{From: subjects[i-1], To: subjects[i]})
			}
		}
		for i := 1; i < len(devices); i++ {
			if rng.Intn(2) == 0 {
				deviceEdges = append(deviceEdges, DeriveEdge{From: devices[i-1], To: devices[i]})
			}
		}

		in := Input{
			Events:         events,
			SubjectDerives: subjectEdges,
			DeviceDerives:  deviceEdges,
		}
		if rng.Intn(2) == 0 {
			rev := Revocation{ID: "rev1", Token: tokens[rng.Intn(len(tokens))]}
			for j := 0; j < n; j++ {
				if rng.Intn(3) == 0 {
					rev.After = append(rev.After, events[j].ID)
				}
			}
			in.Revocations = []Revocation{rev}
		}

		report, err := Analyze(in)
		if err != nil {
			t.Fatalf("iter %d: Analyze error on valid input: %v\ninput: %+v", iter, err, in)
		}
		want := bruteForceVerdict(t, in)
		if report.Verdict != want {
			t.Fatalf("iter %d: Analyze verdict %s, exhaustive oracle %s\ninput: %+v",
				iter, report.Verdict, want, in)
		}
	}
}

// TestLinearExtensionsEnumeratesAllOrders 无约束的 n 个节点应有 n! 个
// 互不相同的拓扑序，验证枚举器本身的完整性。
func TestLinearExtensionsEnumeratesAllOrders(t *testing.T) {
	var events []Event
	tok := token("iss", "digest", 1)
	u1 := inst("svc", 1)
	d1 := inst("host", 1)
	for i := 0; i < 5; i++ {
		events = append(events, Event{
			ID: "e" + string(rune('a'+i)), Kind: KindTokenRead,
			Token: tok, Subject: u1, Device: d1,
		})
	}
	orders, truncated, err := LinearExtensions(Input{Events: events}, 0)
	if err != nil || truncated {
		t.Fatalf("enumeration failed: err=%v truncated=%v", err, truncated)
	}
	if len(orders) != 120 { // 5!
		t.Fatalf("got %d orders, want 120", len(orders))
	}
	seen := make(map[string]bool, len(orders))
	for _, order := range orders {
		if len(order) != 5 {
			t.Fatalf("order length = %d, want 5", len(order))
		}
		key := ""
		for _, id := range order {
			key += id
		}
		if seen[key] {
			t.Fatalf("duplicated topological order: %v", order)
		}
		seen[key] = true
	}
}
