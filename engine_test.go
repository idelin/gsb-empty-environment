package taskpkg

import (
	"bytes"
	"errors"
	"reflect"
	"testing"
)

func TestExpandIntervalsIndependent(t *testing.T) {
	got, err := ExpandIntervals([]Interval{
		{Start: 10, End: 12}, {Start: 0, End: 5}, {Start: 5, End: 9}, {Start: 3, End: 4},
	})
	if err != nil {
		t.Fatalf("ExpandIntervals: %v", err)
	}
	want := []Interval{{Start: 0, End: 9}, {Start: 10, End: 12}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("merged = %v, want %v", got, want)
	}

	shared := []Interval{{Start: 1, End: 2}}
	a, _ := ExpandIntervals(shared)
	b, _ := ExpandIntervals(shared)
	a[0].Start = 99
	if b[0].Start != 1 {
		t.Fatalf("interval expansion leaked across sides: %v", b)
	}

	if _, err := ExpandIntervals([]Interval{{Start: 3, End: 2}}); err == nil {
		t.Fatal("expected error on illegal interval")
	}
	if gaps, err := CoverGap(nil, nil); err != nil || gaps != nil {
		t.Fatalf("empty cover/target: gaps=%v err=%v", gaps, err)
	}
}

func TestCoverGap(t *testing.T) {
	cover := []Interval{{Start: 0, End: 10}, {Start: 20, End: 30}}
	target := []Interval{{Start: 5, End: 25}}
	gaps, err := CoverGap(cover, target)
	if err != nil {
		t.Fatalf("CoverGap: %v", err)
	}
	want := []Interval{{Start: 10, End: 20}}
	if !reflect.DeepEqual(gaps, want) {
		t.Fatalf("gaps = %v, want %v", gaps, want)
	}
	ok, err := ContainsAll(cover, []Interval{{Start: 2, End: 8}, {Start: 22, End: 28}})
	if err != nil || !ok {
		t.Fatalf("ContainsAll = %v, %v", ok, err)
	}
}

func intervalsOverlap(a, b []Interval) bool {
	for _, x := range a {
		for _, y := range b {
			if x.Start < y.End && y.Start < x.End {
				return true
			}
		}
	}
	return false
}

func intervalCovers(cover []Interval, target Interval) bool {
	for _, c := range cover {
		if c.Start <= target.Start && target.End <= c.End {
			return true
		}
	}
	return false
}

func TestSafeBaselineAndDuplicateInsertionBypass(t *testing.T) {
	base := simBody(simEncodedPart("file", "clean.txt", "CLEAN"))

	gateway := newSimParser(simSide{lenient: true, policy: PolicyFirst, auditName: "file"})
	backend := newSimParser(simSide{lenient: true, policy: PolicyLast, auditName: "file"})

	baseline, err := Evaluate(base, gateway, backend)
	if err != nil {
		t.Fatalf("baseline evaluate: %v", err)
	}
	if baseline.Bypassed || baseline.Rejected {
		t.Fatalf("baseline must be safe, got %+v", baseline)
	}

	evil := simEncodedPart("file", "evil.txt", "EVIL")
	closing := simBoundary + "--\r\n"
	candidates := []Candidate{{
		Kind:    KindInsert,
		At:      len(base) - len(closing),
		Payload: []byte(evil),
		Note:    "duplicate file field (evil)",
	}}
	if err := ValidateCandidates(base, candidates); err != nil {
		t.Fatalf("ValidateCandidates: %v", err)
	}

	report, err := (Searcher{Base: base, Candidates: candidates, Gateway: gateway, Backend: backend}).Search()
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if report == nil || report.MinimumChanges != 1 || !reflect.DeepEqual(report.Picks, []int{0}) {
		t.Fatalf("expected single-change bypass, got %+v", report)
	}
	if report.Verdict.Reason != ReasonUncovered || len(report.Uncovered) == 0 {
		t.Fatalf("missing uncovered evidence: %+v", report)
	}
	if !bytes.Contains(report.Mutated, []byte("EVIL")) {
		t.Fatal("mutated sample must contain EVIL bytes")
	}
	gwCover := report.Verdict.Gateway.AuditCover
	beDep := report.Verdict.Backend.Dependency
	if len(gwCover) != 1 || len(beDep) != 1 || intervalsOverlap(gwCover, beDep) {
		t.Fatalf("audited and executed intervals must diverge: gw=%v be=%v", gwCover, beDep)
	}
	if report.Verdict.Gateway.Entries[0].Policy != PolicyFirst ||
		report.Verdict.Backend.Entries[0].Policy != PolicyLast {
		t.Fatal("duplicate policy must be present in evidence")
	}
}

func TestDelimiterReplacementBypass(t *testing.T) {
	first := simEncodedPart("file", "clean.txt", "CLEAN")
	base := simBody(first, simEncodedPart("file", "evil.txt", "EVIL"))

	secondMarker := bytes.Index(base[len(first):], []byte(simBoundary)) + len(first)
	crlfAt := secondMarker - 2
	if !bytes.Equal(base[crlfAt:secondMarker], simCRLF) {
		t.Fatalf("test setup: expected CRLF at %d", crlfAt)
	}

	// 基线两侧同为 first 且都能解析 CRLF 体，覆盖一致、安全；
	// 变异后宽松网关仍看到两个部件（审核首个 CLEAN），严格后端把裸 LF 后的内容
	// 并入首个部件（执行依赖覆盖 EVIL），分歧完全由分隔符替换触发。
	gateway := newSimParser(simSide{lenient: true, policy: PolicyFirst, auditName: "file"})
	backend := newSimParser(simSide{lenient: false, policy: PolicyFirst, auditName: "file"})

	baseline, err := Evaluate(base, gateway, backend)
	if err != nil || baseline.Bypassed {
		t.Fatalf("baseline unsafe: verdict=%+v err=%v", baseline, err)
	}

	candidates := []Candidate{{
		Kind:    KindReplace,
		At:      crlfAt,
		Old:     simCRLF,
		Payload: simLF,
		Note:    "CRLF -> bare LF before second part",
	}}
	report, err := (Searcher{Base: base, Candidates: candidates, Gateway: gateway, Backend: backend}).Search()
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if report == nil {
		t.Fatal("expected delimiter replacement bypass")
	}
	evilAt := bytes.Index(report.Mutated, []byte("EVIL"))
	if evilAt < 0 || !intervalCovers(report.Verdict.Backend.Dependency, Interval{Start: evilAt, End: evilAt + 4}) {
		t.Fatalf("backend dependency must cover EVIL: deps=%v", report.Verdict.Backend.Dependency)
	}
	if intervalCovers(report.Verdict.Gateway.AuditCover, Interval{Start: evilAt, End: evilAt + 4}) {
		t.Fatalf("gateway must not audit EVIL: cover=%v", report.Verdict.Gateway.AuditCover)
	}
}

func TestFieldOrderingDifferenceDoesNotAlert(t *testing.T) {
	base := simBody(
		simEncodedPart("zeta", "", "ZZZ"),
		simEncodedPart("alpha", "", "AAA"),
		simEncodedPart("beta", "", "BBB"),
	)
	ordered := newSimParser(simSide{lenient: true, policy: PolicyAll, reorder: true})
	natural := newSimParser(simSide{lenient: true, policy: PolicyAll, reorder: false})

	verdict, err := Evaluate(base, ordered, natural)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if verdict.Bypassed {
		t.Fatalf("pure ordering difference must not alert: %+v", verdict)
	}
	if verdict.Gateway.Fields[0].Name == verdict.Backend.Fields[0].Name {
		t.Fatal("test setup: field order should differ between sides")
	}
	ok, err := ContainsAll(verdict.Gateway.AuditCover, verdict.Backend.Dependency)
	if err != nil || !ok {
		t.Fatalf("coverage must be identical despite ordering: ok=%v err=%v", ok, err)
	}
}

func TestMinimumChangeSearchWithTwelveCandidates(t *testing.T) {
	base := []byte("PREFIX-BODY-SUFFIX")
	const (
		s1 = "[S1]"
		s2 = "[S2]"
	)
	gateway := Parser(func(body []byte) (*Report, error) {
		r := &Report{Entries: []Entry{{
			Name:   "upload",
			Policy: PolicyAll,
			Chosen: FieldOccurrence{Name: "upload", Raw: Interval{0, len(body)}, Kept: true},
		}}}
		i1 := bytes.Index(body, []byte(s1))
		i2 := bytes.Index(body, []byte(s2))
		if i1 >= 0 && i2 >= 0 && i1+len(s1) < i2 {
			r.Audited = []Interval{{0, i1 + len(s1)}, {i2, len(body)}}
		} else {
			r.Audited = []Interval{{0, len(body)}}
		}
		r.Entries[0].DependOn = r.Audited
		return r, nil
	})
	backend := Parser(func(body []byte) (*Report, error) {
		return &Report{
			Fields: []FieldOccurrence{{Name: "upload", Raw: Interval{0, len(body)}, Kept: true}},
			Entries: []Entry{{
				Name:     "upload",
				Policy:   PolicyAll,
				Chosen:   FieldOccurrence{Name: "upload", Raw: Interval{0, len(body)}, Kept: true},
				DependOn: []Interval{{0, len(body)}},
			}},
		}, nil
	})

	var candidates []Candidate
	for i := 0; i < 10; i++ {
		candidates = append(candidates, Candidate{
			Kind: KindInsert, At: i, Payload: []byte("x"), Note: "benign insert",
		})
	}
	candidates = append(candidates,
		Candidate{Kind: KindInsert, At: 6, Payload: []byte(s1), Note: "sentinel one"},
		Candidate{Kind: KindInsert, At: 12, Payload: []byte(s2), Note: "sentinel two"},
	)
	if len(candidates) != 12 {
		t.Fatalf("expected 12 candidates, got %d", len(candidates))
	}

	report, err := (Searcher{Base: base, Candidates: candidates, Gateway: gateway, Backend: backend}).Search()
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if report == nil {
		t.Fatal("expected two-change bypass")
	}
	if report.MinimumChanges != 2 || !reflect.DeepEqual(report.Picks, []int{10, 11}) {
		t.Fatalf("minimum change = %d picks=%v, want [10 11]", report.MinimumChanges, report.Picks)
	}
	if len(report.Uncovered) == 0 || report.Verdict.Reason != ReasonUncovered {
		t.Fatalf("expected uncovered gap evidence, got %+v", report)
	}
}

func TestRejectionIsNotBypass(t *testing.T) {
	base := []byte("data")
	gateway := Parser(func(body []byte) (*Report, error) {
		return &Report{Rejected: true}, nil
	})
	backend := Parser(func(body []byte) (*Report, error) {
		return &Report{Entries: []Entry{{Name: "f", DependOn: []Interval{{0, len(body)}}}}}, nil
	})
	verdict, err := Evaluate(base, gateway, backend)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if verdict.Bypassed || !verdict.Rejected {
		t.Fatalf("fail-closed rejection must not be a bypass: %+v", verdict)
	}
}

func TestUnsafeBaselineAndErrors(t *testing.T) {
	base := []byte("abcdef")
	gateway := Parser(func(body []byte) (*Report, error) {
		return &Report{Audited: []Interval{{0, 2}}}, nil
	})
	backend := Parser(func(body []byte) (*Report, error) {
		return &Report{Entries: []Entry{{Name: "f", DependOn: []Interval{{0, len(body)}}}}}, nil
	})
	s := Searcher{
		Base:       base,
		Candidates: []Candidate{{Kind: KindInsert, At: 0, Payload: []byte("x")}},
		Gateway:    gateway,
		Backend:    backend,
	}
	if _, err := s.Search(); !errors.Is(err, ErrUnsafeBaseline) {
		t.Fatalf("expected ErrUnsafeBaseline, got %v", err)
	}

	many := make([]Candidate, 13)
	if err := ValidateCandidates(base, many); !errors.Is(err, ErrTooManyCandidates) {
		t.Fatalf("expected ErrTooManyCandidates, got %v", err)
	}

	bad := []Candidate{{Kind: KindReplace, At: 0, Old: []byte("zz"), Payload: []byte("xx")}}
	if err := ValidateCandidates(base, bad); err == nil {
		t.Fatal("expected mismatch error")
	}

	ow := Parser(func(body []byte) (*Report, error) {
		return &Report{Audited: []Interval{{0, len(body) + 1}}}, nil
	})
	if _, err := Evaluate(base, ow, backend); err == nil {
		t.Fatal("expected out-of-range interval error")
	}

	boom := errors.New("boom")
	_, err := Evaluate(base, Parser(func([]byte) (*Report, error) { return nil, boom }), backend)
	if !errors.Is(err, boom) {
		t.Fatalf("expected wrapped parser error, got %v", err)
	}
}

func TestApplyCandidatesDoesNotAliasBase(t *testing.T) {
	base := []byte("0123456789")
	candidates := []Candidate{
		{Kind: KindInsert, At: 2, Payload: []byte("INS")},
		{Kind: KindReplace, At: 5, Old: []byte("56"), Payload: []byte("RR")},
	}
	out, err := ApplyCandidates(base, candidates, []int{0, 1})
	if err != nil {
		t.Fatalf("ApplyCandidates: %v", err)
	}
	if want := "01INS234RR789"; string(out) != want {
		t.Fatalf("mutated = %q, want %q", out, want)
	}
	out[0] = 'Z'
	if base[0] != '0' {
		t.Fatal("ApplyCandidates must return an independent slice")
	}
	if _, err := ApplyCandidates(base, candidates, []int{7}); err == nil {
		t.Fatal("expected out-of-range candidate index error")
	}
}

func TestEnumerateCombinationsOrder(t *testing.T) {
	var seen [][]int
	enumerateCombinations(4, 2, func(picks []int) bool {
		seen = append(seen, append([]int(nil), picks...))
		return true
	})
	want := [][]int{{0, 1}, {0, 2}, {0, 3}, {1, 2}, {1, 3}, {2, 3}}
	if !reflect.DeepEqual(seen, want) {
		t.Fatalf("combinations = %v, want %v", seen, want)
	}
}

func TestSearchReturnsNilWhenNoBypass(t *testing.T) {
	base := []byte("safe-body")
	fullCover := Parser(func(body []byte) (*Report, error) {
		iv := Interval{0, len(body)}
		return &Report{
			Fields:  []FieldOccurrence{{Name: "f", Raw: iv, Kept: true}},
			Entries: []Entry{{Name: "f", Policy: PolicyAll, Chosen: FieldOccurrence{Raw: iv, Kept: true}, DependOn: []Interval{iv}}},
			Audited: []Interval{iv},
		}, nil
	})
	candidates := []Candidate{{Kind: KindInsert, At: 2, Payload: []byte("x")}}
	report, err := (Searcher{Base: base, Candidates: candidates, Gateway: fullCover, Backend: fullCover}).Search()
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if report != nil {
		t.Fatalf("expected nil report when no subset bypasses, got %+v", report)
	}
}

func TestNoBackendEntryIsNotBypass(t *testing.T) {
	base := []byte("anything")
	gateway := Parser(func(body []byte) (*Report, error) {
		return &Report{Audited: []Interval{{0, 1}}}, nil
	})
	backend := Parser(func(body []byte) (*Report, error) {
		return &Report{}, nil
	})
	verdict, err := Evaluate(base, gateway, backend)
	if err != nil || verdict.Bypassed {
		t.Fatalf("backend without entry must not bypass: %+v err=%v", verdict, err)
	}
}

func TestCandidateRelationValidation(t *testing.T) {
	base := []byte("0123456789")
	overlap := []Candidate{
		{Kind: KindReplace, At: 1, Old: []byte("123"), Payload: []byte("aa")},
		{Kind: KindReplace, At: 2, Old: []byte("234"), Payload: []byte("bb")},
	}
	if err := ValidateCandidates(base, overlap); err == nil {
		t.Fatal("expected overlapping replacement error")
	}

	inside := []Candidate{
		{Kind: KindReplace, At: 1, Old: []byte("123"), Payload: []byte("aa")},
		{Kind: KindInsert, At: 2, Payload: []byte("x")},
	}
	if err := ValidateCandidates(base, inside); err == nil {
		t.Fatal("expected insert-inside-replacement error")
	}

	// 插入落在替换边界上是允许的，且同位置插入按候选索引升序写入。
	boundary := []Candidate{
		{Kind: KindReplace, At: 3, Old: []byte("34"), Payload: []byte("RR")},
		{Kind: KindInsert, At: 3, Payload: []byte("A")},
		{Kind: KindInsert, At: 3, Payload: []byte("B")},
	}
	out, err := ApplyCandidates(base, boundary, []int{2, 1, 0})
	if err != nil {
		t.Fatalf("boundary insertion: %v", err)
	}
	if want := "012ABRR56789"; string(out) != want {
		t.Fatalf("boundary insertion order = %q, want %q", out, want)
	}
}

func TestPolicyAllKeepsEveryOccurrence(t *testing.T) {
	base := simBody(
		simEncodedPart("file", "a.txt", "AAA"),
		simEncodedPart("file", "b.txt", "BBB"),
	)
	gw := newSimParser(simSide{lenient: true, policy: PolicyAll, auditName: "file"})
	be := newSimParser(simSide{lenient: true, policy: PolicyAll, auditName: "file"})
	verdict, err := Evaluate(base, gw, be)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if verdict.Bypassed {
		t.Fatalf("PolicyAll on both sides must cover every occurrence: %+v", verdict)
	}
	if len(verdict.Backend.Dependency) != 2 {
		t.Fatalf("expected two merged dependency intervals, got %v", verdict.Backend.Dependency)
	}
	for _, f := range verdict.Backend.Fields {
		if !f.Kept {
			t.Fatal("PolicyAll must mark every occurrence as kept")
		}
	}
}

func TestDuplicatePolicyStringer(t *testing.T) {
	cases := map[DuplicatePolicy]string{
		PolicyUnknown: "unknown",
		PolicyFirst:   "first",
		PolicyLast:    "last",
		PolicyAll:     "all",
	}
	for p, want := range cases {
		if p.String() != want {
			t.Fatalf("policy %d string = %q, want %q", p, p.String(), want)
		}
	}
}
