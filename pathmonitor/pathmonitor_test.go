package pathmonitor

import (
	"sort"
	"strings"
	"testing"
)

// cyclicRecoveryGraph 构造题目要求的认证协议图：
// 强认证失败后进入有环的受限恢复流程，另含异常出口和重新认证支路。
func cyclicRecoveryGraph(t *testing.T) (*Monitor, map[string]Edge) {
	t.Helper()
	states := []State{
		{ID: "anon", Level: LevelAnonymous},
		{ID: "primary", Level: 8},
		{ID: "session", Level: LevelAuthenticated, Success: true},
		{ID: "strong", Level: LevelStrong},
		{ID: "vault", Level: LevelStrong, Success: true},
		{ID: "recover1", Level: 2},
		{ID: "recover2", Level: 6},
		{ID: "recover3", Level: LevelAuthenticated},
		{ID: "restricted", Level: LevelAuthenticated, Success: true, Restricted: true, Required: LevelAuthenticated},
		{ID: "backdoor", Level: LevelAuthenticated, Success: true},
		{ID: "locked", Level: 0},
	}
	edges := []Edge{
		{From: "anon", To: "primary", Kind: EdgeNormal, Label: "primary-auth"},
		{From: "primary", To: "session", Kind: EdgeNormal, Label: "password-ok"},
		{From: "primary", To: "strong", Kind: EdgeNormal, Label: "begin-strong"},
		{From: "strong", To: "vault", Kind: EdgeNormal, Label: "strong-ok"},
		{From: "strong", To: "recover1", Kind: EdgeFailure, Label: "strong-failed"},
		{From: "strong", To: "backdoor", Kind: EdgeFailure, Label: "fallback-session"},
		{From: "strong", To: "locked", Kind: EdgeFailure, Label: "lockout"},
		{From: "recover1", To: "recover2", Kind: EdgeRecoveryProof, Label: "email-otp"},
		{From: "recover2", To: "recover3", Kind: EdgeRecoveryProof, Label: "device-proof"},
		{From: "recover2", To: "recover1", Kind: EdgeRecoveryProof, Label: "retry-otp"},
		{From: "recover3", To: "restricted", Kind: EdgeRecoveryProof, Label: "enter-restricted"},
		{From: "recover3", To: "strong", Kind: EdgeReauth, Label: "reauthenticate"},
	}
	byLabel := make(map[string]Edge, len(edges))
	for _, e := range edges {
		byLabel[e.Label] = e
	}
	m, err := New(states, edges, "anon")
	if err != nil {
		t.Fatalf("New() returned error: %v", err)
	}
	return m, byLabel
}

func TestMonitor_CyclicRecoveryAndAbnormalExits(t *testing.T) {
	m, byLabel := cyclicRecoveryGraph(t)
	r := m.Check()

	if r.Truncated {
		t.Fatalf("enumeration truncated at %d paths", r.TotalPaths)
	}
	if r.TotalPaths == 0 {
		t.Fatal("no paths enumerated")
	}

	t.Run("failure does not retain prior strong level", func(t *testing.T) {
		for _, p := range r.PathsTo("restricted") {
			if !p.Tainted {
				t.Fatalf("restricted path must stay tainted: %s", p)
			}
			if p.FinalLevel > LevelAuthenticated {
				t.Fatalf("recovery proof escaped restricted cap: %s final=%d", p, p.FinalLevel)
			}
			for _, d := range p.Downgrades {
				if d.Kind != EdgeFailure || d.Reason != ReasonFailureReset {
					continue
				}
				if d.FromLevel != LevelStrong || d.ToLevel != 2 {
					t.Fatalf("failure edge kept prior level: %+v", d)
				}
			}
		}
	})

	t.Run("tainted fallback to normal login is a violation", func(t *testing.T) {
		vio := r.ViolationsOf(ViolationTaintedSuccess)
		if len(vio) < 1 {
			t.Fatalf("want tainted-success violations, got %d: %+v", len(vio), r.Violations)
		}
		seenExits := map[string]bool{}
		for _, v := range vio {
			seenExits[v.Exit] = true
			if !containsEdge(v.Path, byLabel["fallback-session"]) {
				t.Fatalf("violation path missing failure edge: %s", v.Path)
			}
			if !v.Path.Tainted {
				t.Fatalf("violation path must be tainted: %s", v.Path)
			}
		}
		if !seenExits["backdoor"] {
			t.Fatalf("backdoor missing from tainted-success violations: %+v", vio)
		}
	})

	t.Run("reauth cleans taint and reaches strong exit without violation", func(t *testing.T) {
		var found bool
		for _, p := range r.PathsTo("vault") {
			if !containsEdge(p, byLabel["reauthenticate"]) {
				continue
			}
			found = true
			if p.Tainted {
				t.Fatalf("reauth path still tainted: %s", p)
			}
			if p.FinalLevel != LevelStrong {
				t.Fatalf("reauth final level = %d, want %d", p.FinalLevel, LevelStrong)
			}
			if len(p.Downgrades) == 0 {
				t.Fatalf("reauth path should record the earlier failure downgrade: %s", p)
			}
			failureSeen, reauthSeen := false, false
			for _, d := range p.Downgrades {
				if d.Reason == ReasonFailureReset {
					failureSeen = true
				}
				if d.Kind == EdgeReauth {
					reauthSeen = true
				}
			}
			if !failureSeen || reauthSeen {
				t.Fatalf("reauth path downgrades wrong (want failure reset, no reauth downgrade): %+v",
					p.Downgrades)
			}
		}
		if !found {
			t.Fatal("no reauth path to vault enumerated")
		}
		if len(r.ViolationsOf(ViolationBelowRequired)) != 0 {
			t.Fatalf("unexpected below-required violations: %+v", r.Violations)
		}
	})

	t.Run("restricted recovery exit is flagged abnormal but not a violation", func(t *testing.T) {
		for _, v := range r.ViolationsOf(ViolationTaintedSuccess) {
			if v.Exit == "restricted" {
				t.Fatalf("restricted exit must not count as tainted-success: %+v", v)
			}
		}
		if !abnormalExists(r.AbnormalExits, "restricted", AbnormalTaintedExit) {
			t.Fatalf("restricted exit missing from abnormal exits: %+v", r.AbnormalExits)
		}
	})

	t.Run("lockout is a dead-end abnormal exit", func(t *testing.T) {
		if !abnormalExists(r.AbnormalExits, "locked", AbnormalDeadEnd) {
			t.Fatalf("locked dead-end missing: %+v", r.AbnormalExits)
		}
		var deadEnd Path
		for _, p := range r.Paths {
			if last(p.Nodes) == "locked" {
				deadEnd = p
			}
		}
		if deadEnd.Nodes == nil {
			t.Fatal("no path to locked state")
		}
		if !deadEnd.Tainted || deadEnd.FinalLevel != LevelAnonymous {
			t.Fatalf("lockout path level/taint wrong: %+v", deadEnd)
		}
	})

	t.Run("downgraded paths are returned for every reduced assurance route", func(t *testing.T) {
		if len(r.Downgraded) == 0 {
			t.Fatal("expected downgraded paths, got none")
		}
		exits := map[string]int{}
		for _, p := range r.Downgraded {
			exits[p.Exit]++
			if !p.HasDowngrade() {
				t.Fatalf("reported downgraded path without downgrade: %s", p)
			}
		}
		if exits["restricted"] == 0 {
			t.Fatalf("recovery downgrade routes missing: %+v", exits)
		}
		if exits["vault"] == 0 {
			t.Fatalf("reauth route (failure then re-auth) missing: %+v", exits)
		}
	})

	t.Run("recovery loop is detected and labeled", func(t *testing.T) {
		if len(r.Cycles) == 0 {
			t.Fatal("expected at least one cycle")
		}
		var recoveryCycle *Cycle
		for i := range r.Cycles {
			if r.Cycles[i].Recovery {
				recoveryCycle = &r.Cycles[i]
			}
		}
		if recoveryCycle == nil {
			t.Fatalf("no recovery cycle found: %+v", r.Cycles)
		}
		joined := strings.Join(recoveryCycle.Nodes, ",")
		if !strings.Contains(joined, "recover1") || !strings.Contains(joined, "recover2") {
			t.Fatalf("recovery cycle nodes wrong: %+v", recoveryCycle.Nodes)
		}
	})

	t.Run("exit summary reports weakest cumulative assurance", func(t *testing.T) {
		var restricted, backdoor, vault *ExitInfo
		for i := range r.Exits {
			switch r.Exits[i].State {
			case "restricted":
				restricted = &r.Exits[i]
			case "backdoor":
				backdoor = &r.Exits[i]
			case "vault":
				vault = &r.Exits[i]
			}
		}
		if restricted == nil || backdoor == nil || vault == nil {
			t.Fatalf("missing exit summaries: %+v", r.Exits)
		}
		if restricted.MinFinalLevel != LevelAuthenticated ||
			restricted.MinWeakestLevel != 2 || !restricted.TaintedReachable {
			t.Fatalf("restricted summary wrong: %+v", restricted)
		}
		if backdoor.MinFinalLevel != LevelAuthenticated || backdoor.MinWeakestLevel != 2 {
			t.Fatalf("backdoor summary wrong: %+v", backdoor)
		}
		if vault.MinFinalLevel != LevelStrong || vault.MinWeakestLevel != 2 ||
			!vault.CleanReachable {
			t.Fatalf("vault summary wrong: %+v", vault)
		}
	})

	t.Run("clean login route has no downgrade and full weakest level", func(t *testing.T) {
		var clean Path
		for _, p := range r.PathsTo("session") {
			clean = p
		}
		if clean.Nodes == nil {
			t.Fatal("no path to session")
		}
		if clean.Tainted || clean.HasDowngrade() {
			t.Fatalf("clean login route tainted/downgraded: %+v", clean)
		}
		if clean.WeakestLevel != 8 || clean.FinalLevel != LevelAuthenticated {
			t.Fatalf("clean login levels wrong: %+v", clean)
		}
	})
}

func TestFailureEdgeNeverRetainsPriorLevel(t *testing.T) {
	states := []State{
		{ID: "start", Level: LevelStrong},
		{ID: "high-target", Level: LevelStrong},
		{ID: "low-target", Level: 3, Success: true},
	}
	edges := []Edge{
		{From: "start", To: "high-target", Kind: EdgeFailure, Label: "fail-to-high"},
		{From: "high-target", To: "low-target", Kind: EdgeNormal, Label: "drop"},
	}
	m, err := New(states, edges, "start")
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	r := m.Check()
	if len(r.Paths) != 1 {
		t.Fatalf("want 1 path, got %d", len(r.Paths))
	}
	p := r.Paths[0]
	// 失败边即便指向同等级目标，也必须置污染；它不“保留”先前等级，
	// 而是重新采用目标状态声明的等级（此处两值恰好相同）。
	if !p.Tainted {
		t.Fatalf("failure edge to equal-level target must still taint: %s", p)
	}
	if p.FinalLevel != 3 {
		t.Fatalf("final level = %d, want 3", p.FinalLevel)
	}
	for _, d := range p.Downgrades {
		if d.Reason == ReasonFailureReset {
			t.Fatalf("equal-level failure reset must not fabricate a downgrade: %+v", d)
		}
	}
	if len(p.Downgrades) != 1 || p.Downgrades[0].Reason != ReasonNormalDecrease {
		t.Fatalf("want single normal downgrade, got %+v", p.Downgrades)
	}
}

func TestFailureResetToLowerDeclaredLevelIsDowngrade(t *testing.T) {
	states := []State{
		{ID: "strong", Level: LevelStrong},
		{ID: "recovery", Level: 4, Success: true},
	}
	edges := []Edge{{From: "strong", To: "recovery", Kind: EdgeFailure, Label: "fail"}}
	m, err := New(states, edges, "strong")
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	r := m.Check()
	p := r.Paths[0]
	if len(p.Downgrades) != 1 {
		t.Fatalf("want 1 downgrade, got %+v", p.Downgrades)
	}
	d := p.Downgrades[0]
	if d.Reason != ReasonFailureReset || d.FromLevel != LevelStrong || d.ToLevel != 4 {
		t.Fatalf("failure reset downgrade wrong: %+v", d)
	}
	if p.WeakestLevel != 4 || p.FinalLevel != 4 {
		t.Fatalf("weakest/final level wrong: %+v", p)
	}
}

func TestBelowRequiredExitViolation(t *testing.T) {
	states := []State{
		{ID: "in", Level: LevelStrong},
		{ID: "weak-exit", Level: 5, Success: true, Required: LevelAuthenticated},
	}
	edges := []Edge{{From: "in", To: "weak-exit", Kind: EdgeFailure, Label: "fail-open"}}
	m, err := New(states, edges, "in")
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	r := m.Check()
	vio := r.ViolationsOf(ViolationBelowRequired)
	if len(vio) != 1 {
		t.Fatalf("want 1 below-required violation, got %+v", r.Violations)
	}
	if vio[0].Exit != "weak-exit" || vio[0].Path.FinalLevel != 5 {
		t.Fatalf("below-required violation wrong: %+v", vio[0])
	}
}

func TestMaxPathsTruncation(t *testing.T) {
	m, _ := cyclicRecoveryGraph(t)
	limited, err := New(m.stateList(), m.edgeList(), m.start, WithMaxPaths(2))
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	r := limited.Check()
	if !r.Truncated {
		t.Fatal("expected truncation flag")
	}
	if len(r.ViolationsOf(ViolationPathLimit)) != 1 {
		t.Fatalf("want path-limit violation, got %+v", r.Violations)
	}
	if len(r.Paths) != 2 {
		t.Fatalf("want exactly 2 paths, got %d", len(r.Paths))
	}
}

func TestNewValidation(t *testing.T) {
	if _, err := New(nil, nil, "x"); err == nil {
		t.Fatal("empty states should be rejected")
	}
	base := []State{
		{ID: "a", Level: 1},
		{ID: "b", Level: 2, Success: true, Required: 9},
	}
	m2, err := New(base, []Edge{{From: "a", To: "b", Kind: EdgeNormal}}, "a")
	if err != nil {
		t.Fatalf("required above declared is allowed for runtime checks: %v", err)
	}
	if got := m2.Check().ViolationsOf(ViolationBelowRequired); len(got) == 0 {
		t.Fatal("under-required success exit should be reported at Check time")
	}
	restrictedOnly := []State{{ID: "a", Level: 1, Restricted: true}}
	if _, err := New(restrictedOnly, nil, "a"); err == nil {
		t.Fatal("restricted flag on non-success state should be rejected")
	}
	good := []State{{ID: "a", Level: 1}}
	if _, err := New(good, nil, "missing"); err == nil {
		t.Fatal("unknown start should be rejected")
	}
	if _, err := New(good, []Edge{{From: "a", To: "ghost", Kind: EdgeNormal}}, "a"); err == nil {
		t.Fatal("edge to unknown state should be rejected")
	}
}

func containsEdge(p Path, want Edge) bool {
	label := want.key()
	for _, got := range p.Edges {
		if got == label {
			return true
		}
	}
	return false
}

func abnormalExists(got []AbnormalExit, state, kind string) bool {
	for _, a := range got {
		if a.State == state && a.Kind == kind {
			return true
		}
	}
	return false
}

func last(slice []string) string {
	if len(slice) == 0 {
		return ""
	}
	return slice[len(slice)-1]
}

// stateList 和 edgeList 让测试可以基于同一图重新构造受限监视器。
func (m *Monitor) stateList() []State {
	ids := make([]string, 0, len(m.states))
	for id := range m.states {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]State, 0, len(ids))
	for _, id := range ids {
		out = append(out, m.states[id])
	}
	return out
}

func (m *Monitor) edgeList() []Edge {
	var out []Edge
	for from := range m.adj {
		out = append(out, m.adj[from]...)
	}
	return out
}
