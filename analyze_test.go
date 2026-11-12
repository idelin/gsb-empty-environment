package netsec

import "testing"

func edge(from, to string, kind EdgeKind) Edge { return Edge{From: from, To: to, Kind: kind} }
func block(id string, ops ...Op) *Block        { return &Block{ID: id, Ops: ops} }

func findingKinds(report *Report) map[FindingKind]int {
	out := map[FindingKind]int{}
	for _, finding := range report.Findings {
		out[finding.Kind] += finding.Count
	}
	return out
}

func assertFinding(t *testing.T, report *Report, kind FindingKind, want bool) {
	t.Helper()
	if got := report.HasFinding(kind); got != want {
		t.Fatalf("finding %s presence = %v, want %v (findings: %v)",
			kind, got, want, report.Findings)
	}
}

// A success and a failure branch: the explicit denial on the failure path
// must not be reported, and the success path must be clean.
func TestAnalyzeCleanDenial(t *testing.T) {
	g := &CFG{
		Entry: "auth",
		Blocks: map[string]*Block{
			"auth":  block("auth", OpAuth{Site: "Authenticate", CredVar: "cred", ErrVar: "err"}),
			"ok":    block("ok", OpProtectedCallback{Site: "cb", Name: "DoPrivileged", Target: "cred"}),
			"okRet": block("okRet", OpReturn{Site: "ok-return"}),
			"deny":  block("deny", OpReturn{Site: "deny-return", Denied: true, ErrVar: "err"}),
		},
		Edges: []Edge{
			edge("auth", "ok", EdgeNormal),
			edge("auth", "deny", EdgeException),
			edge("ok", "okRet", EdgeNormal),
		},
	}

	report, err := Analyze(g, Limits{})
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	if len(report.Paths) != 2 {
		t.Fatalf("paths = %d, want 2", len(report.Paths))
	}
	for _, kind := range []FindingKind{
		FindingErrorMasked,
		FindingRejectionConverted,
		FindingUnauthorizedCallback,
	} {
		assertFinding(t, report, kind, false)
	}

	var callbackAllowed bool
	for _, path := range report.Paths {
		for _, event := range path.Events {
			if event.Kind == EventProtectedCallback {
				callbackAllowed = event.State.Authed && !event.State.Failed
			}
		}
	}
	if !callbackAllowed {
		t.Fatal("protected callback on the success path was not marked allowed")
	}
}

// err = nil followed by a success-shaped return: masking plus conversion.
func TestAnalyzeErrorClearedThenSuccess(t *testing.T) {
	g := &CFG{
		Entry: "auth",
		Blocks: map[string]*Block{
			"auth":  block("auth", OpAuth{Site: "Authenticate", ErrVar: "err"}),
			"clear": block("clear", OpErrorAssign{Site: "clear", Var: "err", Source: "nil", Clear: true}),
			"ret":   block("ret", OpReturn{Site: "swallowed-return"}),
		},
		Edges: []Edge{
			edge("auth", "ret", EdgeNormal),
			edge("auth", "clear", EdgeException),
			edge("clear", "ret", EdgeNormal),
		},
	}

	report, err := Analyze(g, Limits{})
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	assertFinding(t, report, FindingErrorMasked, true)
	assertFinding(t, report, FindingRejectionConverted, true)

	kinds := findingKinds(report)
	if kinds[FindingRejectionConverted] != 1 {
		t.Fatalf("rejection-converted count = %d, want 1", kinds[FindingRejectionConverted])
	}

	// The merge block must record both kinds of incoming flow.
	var mergedAt string
	for _, info := range report.Merges {
		if info.Block == "ret" {
			mergedAt = info.Block
			if !info.ReachableViaException || !info.HasNormalIn {
				t.Fatalf("merge at ret missing runtime flow kinds: %+v", info)
			}
		}
	}
	if mergedAt == "" {
		t.Fatal("expected merge info for block ret")
	}
}

// A later explicit authentication success clears a prior failure; no finding.
func TestAnalyzeNewAuthSuccessClearsFailure(t *testing.T) {
	g := &CFG{
		Entry: "auth1",
		Blocks: map[string]*Block{
			"auth1": block("auth1", OpAuth{Site: "Auth1", ErrVar: "err1"}),
			"retry": block("retry", OpAuth{Site: "Auth2", CredVar: "cred2", ErrVar: "err2"}),
			"ret":   block("ret", OpReturn{Site: "retry-return"}),
		},
		Edges: []Edge{
			edge("auth1", "ret", EdgeNormal),
			edge("auth1", "retry", EdgeException),
			edge("retry", "ret", EdgeNormal),
			edge("retry", "ret", EdgeException),
		},
	}

	report, err := Analyze(g, Limits{})
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	if len(report.Paths) != 3 {
		t.Fatalf("paths = %d, want 3", len(report.Paths))
	}
	assertFinding(t, report, FindingErrorMasked, false)

	// The path failure -> retry success must end with a valid credential.
	var sawRecovered bool
	for _, path := range report.Paths {
		for _, event := range path.Events {
			if event.Kind == EventAuthSuccess && event.Site == "Auth2" {
				if event.State.Authed && !event.State.Failed {
					sawRecovered = true
				}
			}
		}
	}
	if !sawRecovered {
		t.Fatal("retry success did not clear the prior failure")
	}
}

// Recovery must not erase the sticky failure; dropping the recovered value
// masks it and a success return converts the rejection.
func TestAnalyzePanicRecoveryDropped(t *testing.T) {
	g := &CFG{
		Entry: "auth",
		Blocks: map[string]*Block{
			"auth":    block("auth", OpAuth{Site: "PanickyAuth"}),
			"recover": block("recover", OpRecover{Site: "defer-recover"}),
			"ret":     block("ret", OpReturn{Site: "recovered-return"}),
		},
		Edges: []Edge{
			edge("auth", "ret", EdgeNormal),
			edge("auth", "recover", EdgeException),
			edge("recover", "ret", EdgeNormal),
		},
	}

	report, err := Analyze(g, Limits{})
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	assertFinding(t, report, FindingErrorMasked, true)
	assertFinding(t, report, FindingRejectionConverted, true)
}

// Recovery that rebinds and returns the auth error surfaces the failure.
func TestAnalyzePanicRecoverySurfaced(t *testing.T) {
	g := &CFG{
		Entry: "auth",
		Blocks: map[string]*Block{
			"auth":    block("auth", OpAuth{Site: "PanickyAuth"}),
			"recover": block("recover", OpRecover{Site: "defer-recover", ErrVar: "err"}),
			"ret":     block("ret", OpReturn{Site: "deny", Denied: true, ErrVar: "err"}),
		},
		Edges: []Edge{
			edge("auth", "ret", EdgeNormal),
			edge("auth", "recover", EdgeException),
			edge("recover", "ret", EdgeNormal),
		},
	}

	report, err := Analyze(g, Limits{})
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	assertFinding(t, report, FindingErrorMasked, false)
	assertFinding(t, report, FindingRejectionConverted, false)
}

// A protected callback reached on an exception path runs without a credential.
func TestAnalyzeUnauthorizedProtectedCallback(t *testing.T) {
	g := &CFG{
		Entry: "auth",
		Blocks: map[string]*Block{
			"auth": block("auth", OpAuth{Site: "Authenticate", ErrVar: "err"}),
			"cb":   block("cb", OpProtectedCallback{Site: "cb", Name: "Billing.Charge"}),
			"ret":  block("ret", OpReturn{Site: "ret", ErrVar: "err"}),
		},
		Edges: []Edge{
			edge("auth", "ret", EdgeNormal),
			edge("auth", "cb", EdgeException),
			edge("cb", "ret", EdgeNormal),
		},
	}

	report, err := Analyze(g, Limits{})
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	assertFinding(t, report, FindingUnauthorizedCallback, true)
	// The auth error is still returned, so no conversion occurs.
	assertFinding(t, report, FindingRejectionConverted, false)

	var failurePath *PathResult
	for i := range report.Paths {
		if len(report.Paths[i].EdgeKinds) > 0 &&
			report.Paths[i].EdgeKinds[0] == EdgeException {
			failurePath = &report.Paths[i]
		}
	}
	if failurePath == nil {
		t.Fatal("no exception path enumerated")
	}
	if !failurePath.Terminal.FailureSurfaced {
		t.Fatal("return carrying the auth error was not marked FailureSurfaced")
	}
}

// A retry loop is unrolled up to the block-visit bound and truncation is
// reported instead of hanging.
func TestAnalyzeLoopIsBounded(t *testing.T) {
	g := &CFG{
		Entry: "auth",
		Blocks: map[string]*Block{
			"auth": block("auth", OpAuth{Site: "LoopAuth", ErrVar: "err"}),
			"ret":  block("ret", OpReturn{Site: "ret"}),
		},
		Edges: []Edge{
			edge("auth", "ret", EdgeNormal),
			edge("auth", "auth", EdgeException),
		},
	}

	report, err := Analyze(g, Limits{MaxBlockVisits: 3})
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	if !report.Truncated {
		t.Fatal("expected truncation on cyclic graph")
	}
	if report.LimitReason == "" {
		t.Fatal("truncated report missing reason")
	}
	for _, path := range report.Paths {
		visits := 0
		for _, id := range path.Blocks {
			if id == "auth" {
				visits++
			}
		}
		if visits > 3 {
			t.Fatalf("block visited %d times, bound is 3", visits)
		}
	}
}

// Overwriting the auth error with a non-nil value still masks it.
func TestAnalyzeOverwriteMasksAuthError(t *testing.T) {
	g := &CFG{
		Entry: "auth",
		Blocks: map[string]*Block{
			"auth": block("auth", OpAuth{Site: "Authenticate", ErrVar: "err"}),
			"wrap": block("wrap", OpErrorAssign{Site: "wrap", Var: "err", Source: "otherErr"}),
			"ret":  block("ret", OpReturn{Site: "ret"}),
		},
		Edges: []Edge{
			edge("auth", "ret", EdgeNormal),
			edge("auth", "wrap", EdgeException),
			edge("wrap", "ret", EdgeNormal),
		},
	}

	report, err := Analyze(g, Limits{})
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	assertFinding(t, report, FindingErrorMasked, true)
	assertFinding(t, report, FindingRejectionConverted, true)
}

// Explicit denial on a failed path is an intentional rejection, not a finding.
func TestAnalyzeExplicitDenialIsClean(t *testing.T) {
	g := &CFG{
		Entry: "auth",
		Blocks: map[string]*Block{
			"auth": block("auth", OpAuth{Site: "Authenticate", ErrVar: "err"}),
			"deny": block("deny", OpReturn{Site: "deny", Denied: true, ErrVar: "err"}),
			"ok":   block("ok", OpReturn{Site: "ok"}),
		},
		Edges: []Edge{
			edge("auth", "ok", EdgeNormal),
			edge("auth", "deny", EdgeException),
		},
	}

	report, err := Analyze(g, Limits{})
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	for _, kind := range []FindingKind{
		FindingErrorMasked,
		FindingRejectionConverted,
		FindingUnauthorizedCallback,
	} {
		assertFinding(t, report, kind, false)
	}
}

func TestAnalyzeRejectsInvalidCFG(t *testing.T) {
	g := &CFG{
		Entry: "auth",
		Blocks: map[string]*Block{
			"auth": block("auth", OpAuth{Site: "Authenticate", ErrVar: "err"}),
		},
		Edges: []Edge{
			edge("auth", "missing", EdgeException),
		},
	}
	if _, err := Analyze(g, Limits{}); err == nil {
		t.Fatal("expected validation error for dangling edge")
	}
}

// A generic panic must not touch the sticky auth state; recovery that
// rebinds nothing leaves the previously held auth error observable.
func TestAnalyzeGenericPanicPreservesFailure(t *testing.T) {
	g := &CFG{
		Entry: "auth",
		Blocks: map[string]*Block{
			"auth":    block("auth", OpAuth{Site: "Authenticate", ErrVar: "err"}),
			"panic":   block("panic", OpPanic{Site: "boom", Reason: "nil deref"}),
			"recover": block("recover", OpRecover{Site: "defer"}),
			"deny":    block("deny", OpReturn{Site: "deny", Denied: true, ErrVar: "err"}),
		},
		Edges: []Edge{
			edge("auth", "deny", EdgeNormal),
			edge("auth", "panic", EdgeException),
			edge("panic", "recover", EdgeException),
			edge("recover", "deny", EdgeNormal),
		},
	}

	report, err := Analyze(g, Limits{})
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	assertFinding(t, report, FindingErrorMasked, false)
	assertFinding(t, report, FindingRejectionConverted, false)

	var sawRecovered bool
	for _, path := range report.Paths {
		for _, event := range path.Events {
			if event.Kind == EventPanicRecovered && event.State.Failed {
				sawRecovered = true
			}
		}
	}
	if !sawRecovered {
		t.Fatal("generic panic recovery lost the sticky auth failure")
	}
}

func TestValidateRejectsMalformedShapes(t *testing.T) {
	cases := []struct {
		name string
		g    *CFG
	}{
		{
			name: "return block with edge",
			g: &CFG{
				Entry: "a",
				Blocks: map[string]*Block{
					"a": block("a", OpReturn{Site: "ret"}),
					"b": block("b", OpReturn{Site: "ret2"}),
				},
				Edges: []Edge{edge("a", "b", EdgeNormal)},
			},
		},
		{
			name: "auth block missing exception edge",
			g: &CFG{
				Entry: "a",
				Blocks: map[string]*Block{
					"a": block("a", OpAuth{Site: "auth", ErrVar: "err"}),
					"b": block("b", OpReturn{Site: "ret"}),
				},
				Edges: []Edge{edge("a", "b", EdgeNormal)},
			},
		},
		{
			name: "panic block without exception edge",
			g: &CFG{
				Entry: "a",
				Blocks: map[string]*Block{
					"a": block("a", OpPanic{Site: "boom"}),
					"b": block("b", OpReturn{Site: "ret"}),
				},
				Edges: []Edge{edge("a", "b", EdgeNormal)},
			},
		},
		{
			name: "unknown op",
			g: &CFG{
				Entry: "a",
				Blocks: map[string]*Block{
					"a": {ID: "a", Ops: []Op{fakeOp{}}},
				},
			},
		},
		{
			name: "missing entry",
			g: &CFG{
				Entry:  "ghost",
				Blocks: map[string]*Block{"a": block("a", OpReturn{Site: "ret"})},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Analyze(tc.g, Limits{}); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

type fakeOp struct{}

func (fakeOp) opTag() {}
