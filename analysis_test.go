package netsec

import (
	"errors"
	"testing"
)

func secretEqualsPublicBranchProgram(trueValue, falseValue uint8) *Program {
	p := NewProgram()
	p.Inputs = []Input{
		{Register: 0, Labels: Labels("s")},
		{Register: 1, Value: 0},
	}
	p.AddNode(NewEqBranch(0, 1), 1, 2)
	p.AddNode(NewConst(2, trueValue), 3)
	p.AddNode(NewConst(2, falseValue), 3)
	p.AddNode(NewPublicWrite(2), 4)
	p.AddNode(NewReturn())
	return p
}

func TestBranchJoinDiamond(t *testing.T) {
	p := secretEqualsPublicBranchProgram(1, 1)
	info, err := PostDominance(p)
	if err != nil {
		t.Fatal(err)
	}
	if info.VirtualExit != 5 {
		t.Fatalf("virtual exit = %d, want 5", info.VirtualExit)
	}
	if len(info.Branches) != 1 {
		t.Fatalf("branches = %d, want 1", len(info.Branches))
	}
	join := info.Branches[0]
	if join.Join != 3 || !join.Unique || !join.IntroducesPC {
		t.Fatalf("join = %+v, want node 3 unique", join)
	}
	for node, pd := range info.PostDominators {
		if !containsInt(pd, info.VirtualExit) {
			t.Fatalf("node %d does not post-dom virtual exit: %v", node, pd)
		}
	}
}

func TestSameSuccessorDoesNotIntroducePC(t *testing.T) {
	p := NewProgram()
	p.Inputs = []Input{{Register: 0, Labels: Labels("s")}, {Register: 1, Value: 0}}
	p.AddNode(NewEqBranch(0, 1), 1, 1)
	p.AddNode(NewConst(2, 7), 2)
	p.AddNode(NewPublicWrite(2), 3)
	p.AddNode(NewReturn())

	info, err := PostDominance(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Branches) != 1 || info.Branches[0].IntroducesPC {
		t.Fatalf("branch info = %+v, want no PC introduction", info.Branches)
	}
	result := Analyze(p)
	if !result.Safe || result.HasLeak() {
		t.Fatalf("analysis = %+v, want safe", result)
	}
	check := CrossCheck(p)
	if !check.Sound() || len(check.Missed) != 0 {
		t.Fatalf("differential check = %+v", check)
	}
}

func TestBothArmsWriteSameConstant(t *testing.T) {
	p := secretEqualsPublicBranchProgram(7, 7)
	result := Analyze(p)
	if !result.HasLeak() {
		t.Fatalf("analysis = %+v, want conservative leak", result)
	}
	check := CrossCheck(p)
	if len(check.Missed) != 0 || !check.Sound() {
		t.Fatalf("concrete traces should agree: %+v", check)
	}
	if len(check.Conservative) == 0 {
		t.Fatalf("expected conservative false positive, got %+v", check)
	}
}

func TestBothArmsWriteSameConstantInsideArms(t *testing.T) {
	p := NewProgram()
	p.Inputs = []Input{{Register: 0, Labels: Labels("s")}, {Register: 1, Value: 0}}
	p.AddNode(NewEqBranch(0, 1), 1, 2)
	p.AddNode(NewConst(2, 7), 3)
	p.AddNode(NewConst(2, 7), 3)
	p.AddNode(NewPublicWrite(2), 4)
	p.Nodes[3].Edges = []Edge{{To: 4}}
	p.AddNode(NewReturn())

	result := Analyze(p)
	if !result.HasLeak() {
		t.Fatalf("analysis = %+v, want conservative leak", result)
	}
	check := CrossCheck(p)
	if len(check.Missed) != 0 || !check.Sound() {
		t.Fatalf("check = %+v, want allowed false positive", check)
	}
}

func TestStatementCountIsNotTraceEvidence(t *testing.T) {
	p := NewProgram()
	p.Inputs = []Input{{Register: 0, Labels: Labels("s")}, {Register: 1, Value: 0}}
	p.AddNode(NewEqBranch(0, 1), 1, 2)
	p.AddNode(NewConst(2, 7), 3)
	p.AddNode(NewConst(2, 7), 5)
	p.AddNode(Instruction{Op: OpNop}, 4)
	p.AddNode(Instruction{Op: OpNop}, 5)
	p.AddNode(NewPublicWrite(2), 6)
	p.AddNode(NewReturn())

	result := Analyze(p)
	if !result.HasLeak() {
		t.Fatalf("analysis = %+v, want conservative control-flow leak", result)
	}
	check := CrossCheck(p)
	if len(check.Missed) != 0 || len(check.Differences) != 0 || !check.Sound() {
		t.Fatalf("different statement counts should not make traces differ: %+v", check)
	}
}

func TestDifferentBranchConstantsAreCovered(t *testing.T) {
	p := secretEqualsPublicBranchProgram(1, 2)
	result := Analyze(p)
	if !result.HasLeak() {
		t.Fatalf("analysis = %+v, want leak", result)
	}
	check := CrossCheck(p)
	if len(check.Missed) != 0 || check.Covered == 0 || !check.Sound() {
		t.Fatalf("check = %+v, want covered difference", check)
	}
}

func TestEarlyReturnPropagatesPCToExit(t *testing.T) {
	p := NewProgram()
	p.Inputs = []Input{{Register: 0, Labels: Labels("s")}, {Register: 1, Value: 0}}
	p.AddNode(NewEqBranch(0, 1), 1, 2)
	p.AddNode(NewConst(2, 1), 3)
	p.AddNode(NewConst(2, 2), 4)
	p.AddNode(NewPublicWrite(2), 5)
	p.AddNode(NewPublicWrite(2), 6)
	p.AddNode(NewReturn())
	p.Nodes[5].Return = true
	p.AddNode(NewReturn())

	result := Analyze(p)
	if !result.HasLeak() {
		t.Fatalf("analysis = %+v, want leak", result)
	}
	check := CrossCheck(p)
	if len(check.Missed) != 0 || check.Covered == 0 || !check.Sound() {
		t.Fatalf("check = %+v, want covered difference", check)
	}
}

func TestLoopConditionLeaksIntoBody(t *testing.T) {
	p := NewProgram()
	p.Inputs = []Input{
		{Register: 0, Labels: Labels("s")},
		{Register: 3, Value: 0},
		{Register: 2, Value: 7},
	}
	p.AddNode(NewEqBranch(0, 3), 1, 3)
	p.AddNode(NewPublicWrite(2), 2)
	p.AddNode(NewConst(0, 1), 0)
	p.AddNode(NewReturn())

	result := Analyze(p)
	if !result.HasLeak() {
		t.Fatalf("analysis = %+v, want loop-body leak", result)
	}
	check := CrossCheck(p)
	if len(check.Missed) != 0 || check.Covered == 0 || !check.Sound() {
		t.Fatalf("check = %+v, want covered difference", check)
	}
}

func TestSelectAfterBranchKeepsImplicitLabel(t *testing.T) {
	p := NewProgram()
	p.Inputs = []Input{
		{Register: 0, Labels: Labels("s")},
		{Register: 1, Value: 0},
		{Register: 3, Value: 1},
	}
	p.AddNode(NewEqBranch(0, 1), 1, 2)
	p.AddNode(NewConst(2, 5), 3)
	p.AddNode(NewConst(2, 9), 3)
	p.AddNode(NewSelect(4, 3, 2, 3), 4)
	p.AddNode(NewPublicWrite(4), 5)
	p.AddNode(NewReturn())

	result := Analyze(p)
	if !result.HasLeak() {
		t.Fatalf("analysis = %+v, want select leak", result)
	}
	check := CrossCheck(p)
	if len(check.Missed) != 0 || check.Covered == 0 || !check.Sound() {
		t.Fatalf("check = %+v, want select difference covered", check)
	}
}

func TestSelectAfterJoinUsesOnlyLiveLabels(t *testing.T) {
	p := NewProgram()
	p.Inputs = []Input{{Register: 0, Labels: Labels("s")}, {Register: 1, Value: 0}}
	p.AddNode(NewEqBranch(0, 1), 1, 2)
	p.AddNode(Instruction{Op: OpNop}, 3)
	p.AddNode(Instruction{Op: OpNop}, 3)
	p.AddNode(NewConst(2, 7), 4)
	p.AddNode(NewSelect(3, 2, 2, 2), 5)
	p.AddNode(NewPublicWrite(3), 6)
	p.AddNode(NewReturn())

	result := Analyze(p)
	if !result.Safe || result.HasLeak() {
		t.Fatalf("analysis after join = %+v, want safe", result)
	}
}

func TestControlDependentStatementsWithoutWriteAreSafe(t *testing.T) {
	p := NewProgram()
	p.Inputs = []Input{{Register: 0, Labels: Labels("s")}, {Register: 1, Value: 0}}
	p.AddNode(NewEqBranch(0, 1), 1, 2)
	for node := 1; node <= 2; node++ {
		p.AddNode(NewConst(2, 0), 3)
	}
	p.AddNode(NewConst(2, 1), 3)
	p.AddNode(NewConst(3, 2), 3)
	p.AddNode(NewReturn())

	result := Analyze(p)
	if !result.Safe || result.HasLeak() {
		t.Fatalf("analysis = %+v, want safe without public writes", result)
	}
}

func TestAbnormalEdgeCarriesConditionLabel(t *testing.T) {
	p := NewProgram()
	p.Inputs = []Input{{Register: 0, Labels: Labels("s")}, {Register: 1, Value: 0}}
	p.AddNodeWithEdges(NewEqBranch(0, 1),
		Edge{To: 1},
		Edge{To: 2, Kind: AbnormalEdge})
	p.AddNode(NewConst(2, 1), 3)
	p.AddNode(NewConst(2, 2), 3)
	p.AddNode(NewPublicWrite(2), 4)
	p.AddNode(NewReturn())

	result := Analyze(p)
	if !result.HasLeak() || len(result.Leaks) != 1 {
		t.Fatalf("analysis = %+v, want one abnormal-arm leak", result)
	}
}

func TestInfiniteLoopGetsVirtualExit(t *testing.T) {
	p := NewProgram()
	p.AddNode(Instruction{Op: OpNop}, 1)
	p.AddNode(Instruction{Op: OpNop}, 0)
	info, err := PostDominance(p)
	if err != nil {
		t.Fatal(err)
	}
	for node, pd := range info.PostDominators {
		if !containsInt(pd, info.VirtualExit) {
			t.Fatalf("node %d does not reach virtual exit: %v", node, pd)
		}
	}
}

func TestDeclassifyRequiresInstructionAndWhitelist(t *testing.T) {
	p := NewProgram()
	p.Inputs = []Input{{Register: 0, Labels: Labels("s")}}
	p.AddNode(NewDeclassify(0, "s"), 1)
	p.AddNode(NewPublicWrite(0), 2)
	p.AddNode(NewReturn())

	withoutWhitelist := Analyze(p)
	if !withoutWhitelist.HasLeak() {
		t.Fatalf("analysis without whitelist = %+v, want leak", withoutWhitelist)
	}
	if len(withoutWhitelist.Audit) != 1 || !withoutWhitelist.Audit[0].Removed.Empty() {
		t.Fatalf("audit = %+v, want empty removal", withoutWhitelist.Audit)
	}

	withWhitelist := Analyze(p, Options{DeclassifyWhitelist: Labels("s")})
	if !withWhitelist.Safe || withWhitelist.HasLeak() {
		t.Fatalf("analysis with whitelist = %+v, want safe", withWhitelist)
	}
	if len(withWhitelist.Audit) != 1 || !withWhitelist.Audit[0].Removed.Equal(Labels("s")) {
		t.Fatalf("audit = %+v, want s removed", withWhitelist.Audit)
	}

	check := CrossCheck(p, Options{DeclassifyWhitelist: Labels("s")})
	if !check.Sound() || len(check.Missed) != 0 {
		t.Fatalf("declassified difference should be accepted: %+v", check)
	}
	if len(check.Declassified) == 0 {
		t.Fatalf("expected a declassified pair, got %+v", check)
	}
}

func TestDeclassifyCannotClearPC(t *testing.T) {
	p := NewProgram()
	p.Inputs = []Input{{Register: 0, Labels: Labels("s")}, {Register: 1, Value: 0}}
	p.AddNode(NewEqBranch(0, 1), 1, 4)
	p.AddNode(NewConst(2, 7), 2)
	p.AddNode(NewDeclassify(2, "s"), 3)
	p.AddNode(NewPublicWrite(2), 4)
	p.AddNode(NewReturn())

	result := Analyze(p, Options{DeclassifyWhitelist: Labels("s")})
	if !result.HasLeak() {
		t.Fatalf("analysis = %+v, want PC leak", result)
	}
	if len(result.Audit) != 1 || !result.Audit[0].Removed.Equal(Labels("s")) {
		t.Fatalf("audit = %+v, want declared s removed from value", result.Audit)
	}
	if result.Leaks[0].PCLabels.Empty() {
		t.Fatalf("leak = %+v, want non-empty PC", result.Leaks[0])
	}
}

func TestDeclassifyRemovesImplicitValueLabelAtJoin(t *testing.T) {
	p := NewProgram()
	p.Inputs = []Input{{Register: 0, Labels: Labels("s")}, {Register: 1, Value: 0}}
	p.AddNode(NewEqBranch(0, 1), 1, 3)
	p.AddNode(NewConst(2, 7), 2)
	p.AddNode(NewDeclassify(2, "s"), 3)
	p.AddNode(NewPublicWrite(2), 4)
	p.AddNode(NewReturn())

	result := Analyze(p, Options{DeclassifyWhitelist: Labels("s")})
	if !result.Safe || result.HasLeak() {
		t.Fatalf("analysis = %+v, want declassified value to be safe after join", result)
	}
	check := CrossCheck(p, Options{DeclassifyWhitelist: Labels("s")})
	if !check.Sound() || len(check.Missed) != 0 {
		t.Fatalf("check = %+v, want declassified difference accepted", check)
	}
}

func TestDeclassifyWhitelistedLabelNotDeclared(t *testing.T) {
	p := NewProgram()
	p.Inputs = []Input{{Register: 0, Labels: Labels("s", "t")}}
	p.AddNode(NewDeclassify(0, "t"), 1)
	p.AddNode(NewPublicWrite(0), 2)
	p.AddNode(NewReturn())

	result := Analyze(p, Options{DeclassifyWhitelist: Labels("s")})
	if !result.HasLeak() {
		t.Fatalf("analysis = %+v, want leak because t was declared but not whitelisted", result)
	}
	if len(result.Audit) != 1 {
		t.Fatalf("audit = %+v, want one event", result.Audit)
	}
	event := result.Audit[0]
	if !event.Requested.Equal(Labels("t")) || !event.Removed.Empty() ||
		!event.Remaining.Equal(Labels("s", "t")) {
		t.Fatalf("audit event = %+v, want no removal", event)
	}
}

func TestNearestCommonFallsBackToExitWhenAmbiguous(t *testing.T) {
	const exit = 5
	pd := make([]bitSet, exit+1)
	pd[0] = 1<<0 | 1<<2 | 1<<3 | 1<<exit
	pd[1] = 1<<1 | 1<<2 | 1<<3 | 1<<exit
	pd[2] = 1<<2 | 1<<exit
	pd[3] = 1<<3 | 1<<exit
	pd[exit] = 1 << exit

	join, unique := nearestCommonPostdom(0, 1, pd, exit)
	if join != exit || unique {
		t.Fatalf("join=%d unique=%v, want exit with no unique join", join, unique)
	}
}

func TestBudgetExhaustionReturnsUnknown(t *testing.T) {
	p := secretEqualsPublicBranchProgram(1, 2)
	result := Analyze(p, Options{Budget: 1})
	if !result.Unknown || result.Safe {
		t.Fatalf("analysis = %+v, want unknown", result)
	}
	if !errors.Is(result.Err, ErrBudgetExceeded) {
		t.Fatalf("error = %v, want budget", result.Err)
	}
}

func TestNodeLimit(t *testing.T) {
	atLimit := NewProgram()
	for i := 0; i < MaxNodes; i++ {
		atLimit.Nodes = append(atLimit.Nodes, Node{Instr: Instruction{Op: OpNop}})
	}
	if result := Analyze(atLimit); result.Err != nil || result.Unknown {
		t.Fatalf("analysis at node limit = %+v, want accepted", result)
	}

	p := NewProgram()
	for i := 0; i < MaxNodes+1; i++ {
		p.Nodes = append(p.Nodes, Node{Instr: Instruction{Op: OpNop}})
	}
	result := Analyze(p)
	if !result.Unknown || !errors.Is(result.Err, ErrTooManyNodes) {
		t.Fatalf("analysis = %+v, want node-limit error", result)
	}
}

func containsInt(values []int, target int) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
