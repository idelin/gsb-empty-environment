package taskpkg

import "testing"

func inst(name string, n uint64) Instance { return Instance{Name: name, Instance: n} }

func token(issuer, digest string, gen uint64) TokenID {
	return TokenID{Issuer: issuer, Digest: digest, Generation: gen}
}

func ev(id string, k Kind, tok TokenID, subj, dev Instance, session string, after ...string) Event {
	return Event{ID: id, Kind: k, Token: tok, Subject: subj, Device: dev, Session: session, After: after}
}

// 1. 完整链且因果边齐备：每个合法拓扑序中攻击都成立，判必然。
func TestCertainFullChain(t *testing.T) {
	tok := token("iss", "digest", 1)
	u1, u2 := inst("svc", 1), inst("svc", 2)
	d1, d2 := inst("host", 1), inst("host", 2)
	in := Input{
		Events: []Event{
			ev("r", KindTokenRead, tok, u1, d1, ""),
			ev("a", KindTokenAuth, tok, u2, d2, "s1", "r"),
			ev("o", KindProtectedOp, tok, u2, d2, "s1", "a", "v"),
		},
		Revocations:    []Revocation{{ID: "v", Token: tok, After: []string{"a"}}},
		SubjectDerives: []DeriveEdge{{From: u1, To: u2}},
		DeviceDerives:  []DeriveEdge{{From: d1, To: d2}},
	}
	report, err := Analyze(in)
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	if report.Verdict != VerdictCertain {
		t.Fatalf("verdict = %s, want certain", report.Verdict)
	}
	if len(report.Certain) != 1 || len(report.Possible) != 0 {
		t.Fatalf("evidence counts = %d certain / %d possible, want 1/0",
			len(report.Certain), len(report.Possible))
	}
	got := report.Certain[0]
	if got.ReadEventID != "r" || got.AuthEventID != "a" ||
		got.OperationEventID != "o" || got.RevocationID != "v" ||
		len(got.MissingEdges) != 0 {
		t.Fatalf("unexpected evidence: %+v", got)
	}
}

// 2. 同样的三段事件但没有任何因果边：存在使其成立的拓扑序，
// 但缺边，只能判可能，绝不能报确定攻击。
func TestPossibleWhenCausalEdgesMissing(t *testing.T) {
	tok := token("iss", "digest", 1)
	u1, u2 := inst("svc", 1), inst("svc", 2)
	d1, d2 := inst("host", 1), inst("host", 2)
	in := Input{
		Events: []Event{
			ev("r", KindTokenRead, tok, u1, d1, ""),
			ev("a", KindTokenAuth, tok, u2, d2, "s1"),
			ev("o", KindProtectedOp, tok, u2, d2, "s1"),
		},
		Revocations:    []Revocation{{ID: "v", Token: tok}},
		SubjectDerives: []DeriveEdge{{From: u1, To: u2}},
		DeviceDerives:  []DeriveEdge{{From: d1, To: d2}},
	}
	report, err := Analyze(in)
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	if report.Verdict != VerdictPossible {
		t.Fatalf("verdict = %s, want possible", report.Verdict)
	}
	if len(report.Certain) != 0 || len(report.Possible) != 1 {
		t.Fatalf("evidence counts = %d certain / %d possible, want 0/1",
			len(report.Certain), len(report.Possible))
	}
	wantMissing := []OrderedPair{
		{From: "r", To: "a"},
		{From: "a", To: "o"},
		{From: "v", To: "o"},
	}
	got := report.Possible[0].MissingEdges
	if len(got) != len(wantMissing) {
		t.Fatalf("missing edges = %v, want %v", got, wantMissing)
	}
	for i := range wantMissing {
		if got[i] != wantMissing[i] {
			t.Fatalf("missing edge %d = %v, want %v", i, got[i], wantMissing[i])
		}
	}
}

// 3. 不同主体各自的正常事件，无派生关系：不得拼接成链，判不成立。
func TestNotEstablishedAcrossUnrelatedSubjects(t *testing.T) {
	tok := token("iss", "digest", 1)
	u1, u2 := inst("svc", 1), inst("svc", 9)
	d1 := inst("host", 1)
	in := Input{
		Events: []Event{
			ev("r", KindTokenRead, tok, u1, d1, ""),
			ev("a", KindTokenAuth, tok, u2, d1, "s1", "r"),
			ev("o", KindProtectedOp, tok, u2, d1, "s1", "a"),
		},
		Revocations: []Revocation{{ID: "v", Token: tok, After: []string{"a", "r"}}},
	}
	report, err := Analyze(in)
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	if report.Verdict != VerdictNotEstablished ||
		len(report.Certain)+len(report.Possible) != 0 {
		t.Fatalf("got %+v, want no evidence and not-established", report)
	}
}

// 4. 代次不同即不同令牌：换发后的新令牌不能与旧撤销记录拼成链。
func TestNotEstablishedAcrossGenerations(t *testing.T) {
	oldTok := token("iss", "digest", 1)
	newTok := token("iss", "digest", 2)
	u1 := inst("svc", 1)
	d1 := inst("host", 1)
	in := Input{
		Events: []Event{
			ev("r", KindTokenRead, oldTok, u1, d1, ""),
			ev("a", KindTokenAuth, newTok, u1, d1, "s1", "r"),
			ev("o", KindProtectedOp, newTok, u1, d1, "s1", "a"),
		},
		Revocations: []Revocation{{ID: "v", Token: oldTok, After: []string{"r"}}},
	}
	report, err := Analyze(in)
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	if report.Verdict != VerdictNotEstablished {
		t.Fatalf("verdict = %s, want not-established across generations", report.Verdict)
	}
}

// 5. 操作绑定到别的会话：同主体同设备同令牌也不能算作该认证的操作。
func TestNotEstablishedAcrossSessions(t *testing.T) {
	tok := token("iss", "digest", 1)
	u1, u2 := inst("svc", 1), inst("svc", 2)
	d1, d2 := inst("host", 1), inst("host", 2)
	in := Input{
		Events: []Event{
			ev("r", KindTokenRead, tok, u1, d1, ""),
			ev("a", KindTokenAuth, tok, u2, d2, "s1", "r"),
			ev("o", KindProtectedOp, tok, u2, d2, "s9", "a"),
		},
		Revocations:    []Revocation{{ID: "v", Token: tok, After: []string{"a"}}},
		SubjectDerives: []DeriveEdge{{From: u1, To: u2}},
		DeviceDerives:  []DeriveEdge{{From: d1, To: d2}},
	}
	report, err := Analyze(in)
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	if report.Verdict != VerdictNotEstablished {
		t.Fatalf("verdict = %s, want not-established across sessions", report.Verdict)
	}
}

// 6. 主体派生成立但设备谱系断裂：横向跨设备无派生边，拒绝拼接。
func TestNotEstablishedWhenDeviceLineageBroken(t *testing.T) {
	tok := token("iss", "digest", 1)
	u1, u2 := inst("svc", 1), inst("svc", 2)
	d1, d2 := inst("host", 1), inst("host", 9)
	in := Input{
		Events: []Event{
			ev("r", KindTokenRead, tok, u1, d1, ""),
			ev("a", KindTokenAuth, tok, u2, d2, "s1", "r"),
			ev("o", KindProtectedOp, tok, u2, d2, "s1", "a"),
		},
		Revocations:    []Revocation{{ID: "v", Token: tok, After: []string{"a"}}},
		SubjectDerives: []DeriveEdge{{From: u1, To: u2}},
	}
	report, err := Analyze(in)
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	if report.Verdict != VerdictNotEstablished {
		t.Fatalf("verdict = %s, want not-established with broken device lineage", report.Verdict)
	}
}

// 7. 偏序自相矛盾（环）属于宿主输入错误，检测器返回错误而非结论。
func TestCycleInOrderRejected(t *testing.T) {
	tok := token("iss", "digest", 1)
	u1 := inst("svc", 1)
	d1 := inst("host", 1)
	in := Input{
		Events: []Event{
			ev("r", KindTokenRead, tok, u1, d1, "", "a"),
			ev("a", KindTokenAuth, tok, u1, d1, "s1", "r"),
		},
	}
	if _, err := Analyze(in); err == nil {
		t.Fatal("want error for cyclic order, got nil")
	}
}

// 8. 偏序直接保证操作先于撤销：该证据与偏序矛盾，判不成立；
// 另一条撤销先于操作的证据不受影响，仍可判必然。
func TestContradictedCandidateExcluded(t *testing.T) {
	tok := token("iss", "digest", 1)
	u1, u2 := inst("svc", 1), inst("svc", 2)
	d1, d2 := inst("host", 1), inst("host", 2)
	in := Input{
		Events: []Event{
			ev("r", KindTokenRead, tok, u1, d1, ""),
			ev("a", KindTokenAuth, tok, u2, d2, "s1", "r"),
			// o1 被保证在撤销之前，撤销后操作不可能成立。
			ev("o1", KindProtectedOp, tok, u2, d2, "s1", "a"),
			// o2 被保证在撤销之后，构成完整必然证据。
			ev("o2", KindProtectedOp, tok, u2, d2, "s1", "v", "a"),
		},
		Revocations:    []Revocation{{ID: "v", Token: tok, After: []string{"o1", "a"}}},
		SubjectDerives: []DeriveEdge{{From: u1, To: u2}},
		DeviceDerives:  []DeriveEdge{{From: d1, To: d2}},
	}
	report, err := Analyze(in)
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	if report.Verdict != VerdictCertain {
		t.Fatalf("verdict = %s, want certain", report.Verdict)
	}
	if len(report.Certain) != 1 || report.Certain[0].OperationEventID != "o2" {
		t.Fatalf("certain evidence = %+v, want only o2", report.Certain)
	}
}

// 9. 派生关系可多跳传递：读取者沿谱系间接派生到认证主体仍算同一流向。
func TestTransitiveDerivation(t *testing.T) {
	tok := token("iss", "digest", 1)
	u1, u2, u3 := inst("svc", 1), inst("svc", 2), inst("svc", 3)
	d1, d2, d3 := inst("host", 1), inst("host", 2), inst("host", 3)
	in := Input{
		Events: []Event{
			ev("r", KindTokenRead, tok, u1, d1, ""),
			ev("a", KindTokenAuth, tok, u3, d3, "s1", "r"),
			ev("o", KindProtectedOp, tok, u3, d3, "s1", "a", "v"),
		},
		Revocations: []Revocation{{ID: "v", Token: tok, After: []string{"a"}}},
		SubjectDerives: []DeriveEdge{
			{From: u1, To: u2},
			{From: u2, To: u3},
		},
		DeviceDerives: []DeriveEdge{
			{From: d1, To: d2},
			{From: d2, To: d3},
		},
	}
	report, err := Analyze(in)
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	if report.Verdict != VerdictCertain || len(report.Certain) != 1 {
		t.Fatalf("got %+v, want one certain evidence via transitive derivation", report)
	}
}

// 10. 超过 20 个事件直接拒绝，符合输入规模约束。
func TestTooManyEventsRejected(t *testing.T) {
	tok := token("iss", "digest", 1)
	u1 := inst("svc", 1)
	d1 := inst("host", 1)
	in := Input{}
	for i := 0; i < maxEvents+1; i++ {
		id := string(rune('a' + i))
		in.Events = append(in.Events, ev(id, KindTokenRead, tok, u1, d1, ""))
	}
	if _, err := Analyze(in); err == nil {
		t.Fatal("want error for more than 20 events, got nil")
	}
}

// 11. 各类非法输入必须被明确拒绝，而不是产出误导性结论。
func TestInvalidInputsRejected(t *testing.T) {
	tok := token("iss", "digest", 1)
	u1 := inst("svc", 1)
	d1 := inst("host", 1)

	t.Run("duplicated node id", func(t *testing.T) {
		in := Input{Events: []Event{
			ev("e", KindTokenRead, tok, u1, d1, ""),
			ev("e", KindTokenRead, tok, u1, d1, ""),
		}}
		if _, err := Analyze(in); err == nil {
			t.Fatal("want duplicated-id error")
		}
	})

	t.Run("empty node id", func(t *testing.T) {
		in := Input{Events: []Event{
			ev("", KindTokenRead, tok, u1, d1, ""),
		}}
		if _, err := Analyze(in); err == nil {
			t.Fatal("want empty-id error")
		}
	})

	t.Run("unknown order reference", func(t *testing.T) {
		in := Input{Events: []Event{
			ev("e", KindTokenRead, tok, u1, d1, "", "ghost"),
		}}
		if _, err := Analyze(in); err == nil {
			t.Fatal("want unknown-reference error")
		}
	})

	t.Run("self ordering edge", func(t *testing.T) {
		in := Input{Events: []Event{
			ev("e", KindTokenRead, tok, u1, d1, "", "e"),
		}}
		if _, err := Analyze(in); err == nil {
			t.Fatal("want self-edge error")
		}
	})

	t.Run("missing identity", func(t *testing.T) {
		in := Input{Events: []Event{
			{ID: "e", Kind: KindTokenRead, Token: tok},
		}}
		if _, err := Analyze(in); err == nil {
			t.Fatal("want missing-identity error")
		}
	})

	t.Run("operation missing session", func(t *testing.T) {
		in := Input{Events: []Event{
			ev("e", KindProtectedOp, tok, u1, d1, ""),
		}}
		if _, err := Analyze(in); err == nil {
			t.Fatal("want missing-session error")
		}
	})

	t.Run("unknown event kind", func(t *testing.T) {
		in := Input{Events: []Event{
			ev("e", KindUnknown, tok, u1, d1, ""),
		}}
		if _, err := Analyze(in); err == nil {
			t.Fatal("want unknown-kind error")
		}
	})

	t.Run("cyclic subject derivation", func(t *testing.T) {
		u2 := inst("svc", 2)
		in := Input{
			Events:         []Event{ev("e", KindTokenRead, tok, u1, d1, "")},
			SubjectDerives: []DeriveEdge{{From: u1, To: u2}, {From: u2, To: u1}},
		}
		if _, err := Analyze(in); err == nil {
			t.Fatal("want derivation-cycle error")
		}
	})

	t.Run("cyclic device derivation", func(t *testing.T) {
		d2 := inst("host", 2)
		in := Input{
			Events:        []Event{ev("e", KindTokenRead, tok, u1, d1, "")},
			DeviceDerives: []DeriveEdge{{From: d1, To: d2}, {From: d2, To: d1}},
		}
		if _, err := Analyze(in); err == nil {
			t.Fatal("want device derivation-cycle error")
		}
	})

	t.Run("self derivation edge", func(t *testing.T) {
		in := Input{
			Events:         []Event{ev("e", KindTokenRead, tok, u1, d1, "")},
			SubjectDerives: []DeriveEdge{{From: u1, To: u1}},
		}
		if _, err := Analyze(in); err == nil {
			t.Fatal("want self-derivation error")
		}
	})

	t.Run("revocation unknown reference", func(t *testing.T) {
		in := Input{
			Events:      []Event{ev("e", KindTokenRead, tok, u1, d1, "")},
			Revocations: []Revocation{{ID: "v", Token: tok, After: []string{"ghost"}}},
		}
		if _, err := Analyze(in); err == nil {
			t.Fatal("want revocation-reference error")
		}
	})
}

// 12. 空输入是合法边界：没有任何候选证据，结论为不成立。
func TestEmptyInput(t *testing.T) {
	report, err := Analyze(Input{})
	if err != nil {
		t.Fatalf("empty input returned error: %v", err)
	}
	if report.Verdict != VerdictNotEstablished ||
		len(report.Certain) != 0 || len(report.Possible) != 0 {
		t.Fatalf("empty input report = %+v, want not-established with no evidence", report)
	}
}

// 13. 派生路径与枚举截断、字符串方法等辅助行为。
func TestHelpersAndEnumerationTruncation(t *testing.T) {
	u1, u2, u3 := inst("svc", 1), inst("svc", 2), inst("svc", 3)
	l, err := buildLineage([]DeriveEdge{{From: u1, To: u2}, {From: u2, To: u3}})
	if err != nil {
		t.Fatalf("buildLineage error: %v", err)
	}
	got := l.path(u1, u3)
	want := []Instance{u1, u2, u3}
	if len(got) != len(want) {
		t.Fatalf("path = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("path = %v, want %v", got, want)
		}
	}
	if p := l.path(u3, u1); p != nil {
		t.Fatalf("reverse path = %v, want nil", p)
	}
	unknown := inst("svc", 99)
	if l.reaches(u1, unknown) {
		t.Fatal("reaches unknown instance should be false")
	}

	// 枚举达到上限时返回 truncated 标记，避免宿主误以为拿到了全量拓扑序。
	tok := token("iss", "digest", 1)
	d1 := inst("host", 1)
	var events []Event
	for i := 0; i < 6; i++ {
		events = append(events, Event{
			ID: "e" + string(rune('a'+i)), Kind: KindTokenRead,
			Token: tok, Subject: u1, Device: d1,
		})
	}
	orders, truncated, err := LinearExtensions(Input{Events: events}, 10)
	if err != nil || !truncated || len(orders) != 10 {
		t.Fatalf("truncation: err=%v truncated=%v len=%d", err, truncated, len(orders))
	}

	if KindTokenAuth.String() != "token-auth" || VerdictPossible.String() != "possible" ||
		KindUnknown.String() != "unknown" {
		t.Fatal("unexpected stringer output")
	}
}
