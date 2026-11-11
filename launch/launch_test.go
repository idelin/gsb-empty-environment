package launch

import (
	"errors"
	"fmt"
	"slices"
	"testing"
)

// fakeIDs is a static IdentitySource for tests.
type fakeIDs struct {
	users  map[string][2]int // name -> [uid, gid]
	groups map[string]int
}

func (f fakeIDs) LookupUser(name string) (int, int, error) {
	if ug, ok := f.users[name]; ok {
		return ug[0], ug[1], nil
	}
	return 0, 0, fmt.Errorf("unknown user %q", name)
}

func (f fakeIDs) LookupGroup(name string) (int, error) {
	if g, ok := f.groups[name]; ok {
		return g, nil
	}
	return 0, fmt.Errorf("unknown group %q", name)
}

// mockKernel simulates the kernel's per-process security state. It
// enforces privilege rules on every privileged transition (like a real
// kernel would), records a call log, and can be told to fail a specific
// step to exercise partial-failure handling.
type mockKernel struct {
	state  ProcState
	log    []string
	failAt StepKind // if >= 0, this step kind returns an error
	execed bool
}

func newMockKernel(st ProcState) *mockKernel {
	return &mockKernel{state: st, failAt: StepKind(-1)}
}

func (m *mockKernel) State() ProcState { return m.state }

// requirePriv mirrors the kernel check: root or the capability in the
// bounding set.
func (m *mockKernel) requirePriv(cap string) error {
	if m.state.UID == 0 || slices.Contains(m.state.Bounding, cap) {
		return nil
	}
	return ErrPermission
}

func (m *mockKernel) fail(kind StepKind) error {
	if m.failAt == kind {
		return fmt.Errorf("injected fault at %s", kind)
	}
	return nil
}

func (m *mockKernel) SetNoNewPrivs() error {
	m.log = append(m.log, "no_new_privs")
	if err := m.fail(StepNoNewPrivs); err != nil {
		return err
	}
	m.state.NoNewPrivs = true
	return nil
}

func (m *mockKernel) SetBoundingSet(keep []string) error {
	m.log = append(m.log, "cap_bound")
	if err := m.requirePriv("CAP_SETPCAP"); err != nil {
		return err
	}
	if err := m.fail(StepCapBound); err != nil {
		return err
	}
	m.state.Bounding = slices.Clone(keep)
	return nil
}

func (m *mockKernel) SetGroups(gids []int) error {
	m.log = append(m.log, "groups")
	if err := m.requirePriv("CAP_SETGID"); err != nil {
		return err
	}
	if err := m.fail(StepGroups); err != nil {
		return err
	}
	m.state.Groups = slices.Clone(gids)
	return nil
}

func (m *mockKernel) SetGID(gid int) error {
	m.log = append(m.log, "gid")
	if err := m.requirePriv("CAP_SETGID"); err != nil {
		return err
	}
	if err := m.fail(StepGID); err != nil {
		return err
	}
	m.state.GID = gid
	return nil
}

func (m *mockKernel) SetUID(uid int) error {
	m.log = append(m.log, "uid")
	if err := m.requirePriv("CAP_SETUID"); err != nil {
		return err
	}
	if err := m.fail(StepUID); err != nil {
		return err
	}
	m.state.UID = uid
	// Note: the bounding set persists across a UID transition on a real
	// kernel; only permitted/effective caps are cleared, which this mock
	// does not model.
	return nil
}

func (m *mockKernel) Exec(path string, argv, env []string) error {
	m.log = append(m.log, "exec")
	if err := m.fail(StepExec); err != nil {
		return err
	}
	m.execed = true
	return nil
}

var testIDs = fakeIDs{
	users:  map[string][2]int{"plugin-svc": {1000, 1000}},
	groups: map[string]int{"logs": 2001, "net": 2002},
}

func testSpec() Spec {
	return Spec{
		Plugin:     "metrics",
		User:       "plugin-svc",
		Groups:     []string{"logs", "net", "logs"}, // duplicate on purpose
		KeepCaps:   []string{"CAP_NET_BIND_SERVICE"},
		NoNewPrivs: true,
		Path:       "/opt/plugins/metrics",
		Args:       []string{"metrics", "--serve"},
		Env:        []string{"PLUGIN=metrics"},
	}
}

func rootState() ProcState {
	return ProcState{
		UID:      0,
		GID:      0,
		Groups:   []int{0},
		Bounding: []string{"CAP_SETUID", "CAP_SETGID", "CAP_SETPCAP", "CAP_NET_BIND_SERVICE"},
	}
}

func TestPlanLaunchOrderingAndResolution(t *testing.T) {
	plan, err := PlanLaunch(testSpec(), testIDs)
	if err != nil {
		t.Fatalf("PlanLaunch: %v", err)
	}
	wantKinds := []StepKind{StepNoNewPrivs, StepCapBound, StepGroups, StepGID, StepUID, StepExec}
	if len(plan.Steps) != len(wantKinds) {
		t.Fatalf("got %d steps, want %d", len(plan.Steps), len(wantKinds))
	}
	for i, want := range wantKinds {
		if plan.Steps[i].Kind != want {
			t.Errorf("step %d = %s, want %s", i, plan.Steps[i].Kind, want)
		}
	}
	// UID must be the last demotion step before exec.
	if plan.Steps[len(plan.Steps)-2].Kind != StepUID {
		t.Errorf("uid step is not the final demotion step")
	}
	// Groups resolved, deduplicated, primary GID excluded.
	if got := plan.Steps[2].GIDs; !slices.Equal(got, []int{2001, 2002}) {
		t.Errorf("groups = %v, want [2001 2002]", got)
	}
	if plan.Steps[4].UID != 1000 || plan.Steps[3].GID != 1000 {
		t.Errorf("uid/gid = %d/%d, want 1000/1000", plan.Steps[4].UID, plan.Steps[3].GID)
	}
}

func TestPlanLaunchRejectsInvalidSpecs(t *testing.T) {
	cases := map[string]Spec{
		"empty plugin": {User: "plugin-svc", Path: "/x"},
		"empty user":   {Plugin: "p", Path: "/x"},
		"empty path":   {Plugin: "p", User: "plugin-svc"},
		"root user":    {Plugin: "p", User: "root", Path: "/x"},
		"unknown user": {Plugin: "p", User: "ghost", Path: "/x"},
		"unknown grp":  {Plugin: "p", User: "plugin-svc", Path: "/x", Groups: []string{"ghost"}},
	}
	ids := fakeIDs{users: map[string][2]int{"plugin-svc": {1000, 1000}, "root": {0, 0}}}
	for name, spec := range cases {
		if _, err := PlanLaunch(spec, ids); err == nil {
			t.Errorf("%s: expected error, got nil", name)
		}
	}
}

func TestApplyHappyPathOrder(t *testing.T) {
	plan, err := PlanLaunch(testSpec(), testIDs)
	if err != nil {
		t.Fatalf("PlanLaunch: %v", err)
	}
	k := newMockKernel(rootState())
	if err := Apply(k, plan); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	want := []string{"no_new_privs", "cap_bound", "groups", "gid", "uid", "exec"}
	if !slices.Equal(k.log, want) {
		t.Errorf("call log = %v, want %v", k.log, want)
	}
	st := k.State()
	if st.UID != 1000 || st.GID != 1000 {
		t.Errorf("final uid/gid = %d/%d, want 1000/1000", st.UID, st.GID)
	}
	if !st.NoNewPrivs {
		t.Errorf("no_new_privs not set")
	}
	if !slices.Equal(st.Bounding, []string{"CAP_NET_BIND_SERVICE"}) {
		t.Errorf("bounding = %v", st.Bounding)
	}
}

// TestPartialFailureAbortsBeforeExec injects a fault mid-sequence and
// verifies the executor stops immediately: no later step runs, exec is
// never reached, and the process keeps its pre-demotion UID.
func TestPartialFailureAbortsBeforeExec(t *testing.T) {
	for _, failAt := range []StepKind{StepNoNewPrivs, StepCapBound, StepGroups, StepGID, StepUID} {
		t.Run(failAt.String(), func(t *testing.T) {
			plan, err := PlanLaunch(testSpec(), testIDs)
			if err != nil {
				t.Fatalf("PlanLaunch: %v", err)
			}
			k := newMockKernel(rootState())
			k.failAt = failAt
			err = Apply(k, plan)
			if err == nil {
				t.Fatalf("expected error when %s fails", failAt)
			}
			if k.execed {
				t.Fatalf("exec reached despite %s failure", failAt)
			}
			if slices.Contains(k.log, "exec") {
				t.Fatalf("exec syscall issued after failure; log = %v", k.log)
			}
			// The failing step is the last one attempted.
			if got := k.log[len(k.log)-1]; got != failAt.String() {
				t.Fatalf("last attempted step = %s, want %s", got, failAt)
			}
			// No step after the failure may have run, so the UID must
			// not have been dropped mid-sequence by later steps.
			if failAt != StepUID && k.state.UID != 0 {
				t.Fatalf("uid changed to %d despite earlier failure", k.state.UID)
			}
		})
	}
}

// TestInsufficientParentPrivilege verifies that an unprivileged parent
// is rejected during preflight, before any state is mutated.
func TestInsufficientParentPrivilege(t *testing.T) {
	plan, err := PlanLaunch(testSpec(), testIDs)
	if err != nil {
		t.Fatalf("PlanLaunch: %v", err)
	}
	k := newMockKernel(ProcState{
		UID:      1000,
		GID:      1000,
		Groups:   []int{1000},
		Bounding: nil, // no capabilities at all
	})
	err = Apply(k, plan)
	if !errors.Is(err, ErrPermission) {
		t.Fatalf("Apply error = %v, want ErrPermission", err)
	}
	if len(k.log) != 0 {
		t.Fatalf("preflight must reject before any syscall; log = %v", k.log)
	}
	if k.execed {
		t.Fatal("exec reached without privileges")
	}
}

// TestPartialPrivilege verifies a parent holding only some capabilities
// is rejected even though the first demotion steps would have succeeded.
func TestPartialPrivilege(t *testing.T) {
	plan, err := PlanLaunch(testSpec(), testIDs)
	if err != nil {
		t.Fatalf("PlanLaunch: %v", err)
	}
	k := newMockKernel(ProcState{
		UID:      1000,
		GID:      1000,
		Bounding: []string{"CAP_SETPCAP"}, // can drop caps, cannot setuid
	})
	if err := Apply(k, plan); !errors.Is(err, ErrPermission) {
		t.Fatalf("Apply error = %v, want ErrPermission", err)
	}
	if len(k.log) != 0 {
		t.Fatalf("preflight must reject before any mutation; log = %v", k.log)
	}
}

// TestRepeatedApplication applies the same plan twice. After the first
// run the process is fully demoted and holds no privileges; the second
// run must still succeed because every demotion step is already
// satisfied and only exec actually runs.
func TestRepeatedApplication(t *testing.T) {
	plan, err := PlanLaunch(testSpec(), testIDs)
	if err != nil {
		t.Fatalf("PlanLaunch: %v", err)
	}
	k := newMockKernel(rootState())
	if err := Apply(k, plan); err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	firstLog := slices.Clone(k.log)

	k.log = nil
	k.execed = false
	if err := Apply(k, plan); err != nil {
		t.Fatalf("second Apply on demoted state: %v", err)
	}
	if !k.execed {
		t.Fatal("second Apply did not reach exec")
	}
	// Only exec should run the second time; all demotion steps are
	// no-ops on the already-confined state.
	if !slices.Equal(k.log, []string{"exec"}) {
		t.Errorf("second Apply log = %v, want [exec]", k.log)
	}
	if !slices.Equal(firstLog, []string{"no_new_privs", "cap_bound", "groups", "gid", "uid", "exec"}) {
		t.Errorf("first Apply log = %v", firstLog)
	}
}

// TestNoNewPrivsOptional verifies the planner omits the step when the
// spec does not request it, keeping the plan minimal.
func TestNoNewPrivsOptional(t *testing.T) {
	spec := testSpec()
	spec.NoNewPrivs = false
	plan, err := PlanLaunch(spec, testIDs)
	if err != nil {
		t.Fatalf("PlanLaunch: %v", err)
	}
	if plan.Steps[0].Kind != StepCapBound {
		t.Errorf("first step = %s, want cap_bound", plan.Steps[0].Kind)
	}
}
