package netsec

import (
	"strings"
	"testing"
)

func TestTerminalHardeningShortestPlan(t *testing.T) {
	p := TerminalHardeningProblem()
	plan, ev, err := Solve(p)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if ev != nil {
		t.Fatalf("expected plan, got evidence: %v", ev)
	}
	want := []string{"DeployKeys", "StartSSH", "OpenSSHPort", "CloseTelnetPort", "StopTelnet"}
	if len(plan.Steps) != len(want) {
		t.Fatalf("plan length = %d, want %d (shortest)", len(plan.Steps), len(want))
	}
	for i, name := range want {
		if plan.Steps[i].Action != name {
			t.Errorf("step %d = %q, want %q", i, plan.Steps[i].Action, name)
		}
	}
	if err := VerifyPlan(p, plan); err != nil {
		t.Fatalf("VerifyPlan: %v", err)
	}
}

func TestPlanKeepsInvariantsThroughout(t *testing.T) {
	p := TerminalHardeningProblem()
	plan, _, err := Solve(p)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	mgmt := Bit(SettingTelnetUp) | Bit(SettingSSHUp)
	debug := Bit(SettingDebugShell)
	states := append([]State{p.Initial}, stepsToStates(plan)...)
	for i, s := range states {
		if s&mgmt == 0 && !channelUsable(s) {
			t.Errorf("state %d (%#05x) lost every management channel", i, uint32(s))
		}
		if s&debug != 0 {
			t.Errorf("state %d (%#05x) exposed the debug shell", i, uint32(s))
		}
	}
}

func stepsToStates(plan *Plan) []State {
	out := make([]State, 0, len(plan.Steps))
	for _, st := range plan.Steps {
		out = append(out, st.After)
	}
	return out
}

func channelUsable(s State) bool {
	telnet := s&Bit(SettingTelnetUp) != 0 && s&Bit(SettingTelnetPortOpen) != 0
	ssh := s&Bit(SettingSSHUp) != 0 && s&Bit(SettingSSHPortOpen) != 0
	return telnet || ssh
}

func TestPlanNeverUsesDangerousAction(t *testing.T) {
	p := TerminalHardeningProblem()
	plan, _, err := Solve(p)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	for _, st := range plan.Steps {
		if st.Action == "OpenDebugShell" {
			t.Fatalf("plan used forbidden action OpenDebugShell")
		}
	}
}

func TestUnreachableEvidence(t *testing.T) {
	p := TerminalHardeningProblem()
	// Remove DeployKeys: SSH can never start, so the goal is unreachable.
	kept := p.Actions[:0]
	for _, a := range p.Actions {
		if a.Name != "DeployKeys" {
			kept = append(kept, a)
		}
	}
	p.Actions = kept

	plan, ev, err := Solve(p)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if plan != nil {
		t.Fatalf("expected no plan, got %v", plan.Steps)
	}
	if ev == nil {
		t.Fatal("expected unreachability evidence")
	}
	if ev.StatesExplored == 0 {
		t.Error("evidence must report the explored state count")
	}
	if ev.GoalAllMissing&Bit(SettingKeysDeployed) == 0 {
		t.Errorf("evidence should identify KeysDeployed as never set: %v", ev)
	}
	if ev.GoalAllMissing&Bit(SettingSSHUp) == 0 {
		t.Errorf("evidence should identify SSHUp as never set: %v", ev)
	}
}

func TestUnreachableWhenOnlyDangerousPathRemains(t *testing.T) {
	p := TerminalHardeningProblem()
	// Force the goal to require the debug shell: the only action that
	// sets it violates the no-dangerous-entry invariant, so every path
	// to the goal is pruned.
	p.GoalAll |= Bit(SettingDebugShell)
	p.GoalNone &^= Bit(SettingDebugShell)

	plan, ev, err := Solve(p)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if plan != nil {
		t.Fatal("expected no plan when goal requires a forbidden entry")
	}
	if ev == nil || ev.GoalAllMissing&Bit(SettingDebugShell) == 0 {
		t.Fatalf("evidence should blame the debug shell bit: %+v", ev)
	}
}

func TestCheckFullStateSpace(t *testing.T) {
	p := TerminalHardeningProblem()
	res, err := Check(p)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !res.GoalReachable {
		t.Error("goal should be reachable in the scenario")
	}
	if res.ReachableStates == 0 {
		t.Error("expected a non-empty reachable set")
	}
	if res.PrunedTransitions == 0 {
		t.Error("expected OpenDebugShell transitions to be pruned")
	}
	if res.ReachableStates > 1<<uint(p.NumSettings) {
		t.Errorf("reachable set %d exceeds state space %d", res.ReachableStates, 1<<uint(p.NumSettings))
	}
}

func TestVerifyPlanRejectsTampering(t *testing.T) {
	p := TerminalHardeningProblem()
	plan, _, err := Solve(p)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}

	// Dropping the key deployment must break StartSSH's precondition.
	broken := &Plan{Steps: append([]Step{}, plan.Steps[1:]...)}
	if err := VerifyPlan(p, broken); err == nil ||
		!strings.Contains(err.Error(), "preconditions") {
		t.Errorf("expected precondition failure, got %v", err)
	}

	// A fabricated resulting state must be caught.
	lied := &Plan{Steps: append([]Step{}, plan.Steps...)}
	lied.Steps[0].After ^= Bit(SettingDebugShell)
	if err := VerifyPlan(p, lied); err == nil {
		t.Error("expected mismatch on fabricated state")
	}

	// A plan that stops early is not a goal.
	short := &Plan{Steps: append([]Step{}, plan.Steps[:2]...)}
	if err := VerifyPlan(p, short); err == nil ||
		!strings.Contains(err.Error(), "not a goal") {
		t.Errorf("expected goal failure, got %v", err)
	}
}

func TestValidateLimits(t *testing.T) {
	tooMany := &Problem{NumSettings: MaxSettings + 1}
	if _, _, err := Solve(tooMany); err == nil {
		t.Error("expected error for more than MaxSettings settings")
	}

	conflict := &Problem{
		NumSettings: 2,
		Actions:     []Action{{Name: "bad", Set: Bit(0), Clear: Bit(0)}},
	}
	if _, _, err := Solve(conflict); err == nil {
		t.Error("expected error for set/clear conflict")
	}

	unsafe := &Problem{
		NumSettings: 1,
		Initial:     0,
		Invariants:  []Invariant{AtLeastOne("channel", Bit(0))},
	}
	if _, _, err := Solve(unsafe); err == nil {
		t.Error("expected error when initial state violates an invariant")
	}
}

func TestMaxSettingsSolvable(t *testing.T) {
	// 18 settings: a chain where each bit requires the previous one.
	p := &Problem{NumSettings: MaxSettings, GoalAll: Bit(MaxSettings - 1)}
	for i := 0; i < MaxSettings; i++ {
		a := Action{Name: strings.Repeat("a", i+1), Set: Bit(i)}
		if i > 0 {
			a.RequireAll = Bit(i - 1)
		}
		p.Actions = append(p.Actions, a)
	}
	plan, ev, err := Solve(p)
	if err != nil {
		t.Fatalf("Solve: %v", err)
	}
	if ev != nil {
		t.Fatalf("unexpected evidence: %v", ev)
	}
	if len(plan.Steps) != MaxSettings {
		t.Fatalf("plan length = %d, want %d", len(plan.Steps), MaxSettings)
	}
	if err := VerifyPlan(p, plan); err != nil {
		t.Fatalf("VerifyPlan: %v", err)
	}
}
