package upgradeplan

import (
	"fmt"
	"sort"
	"strings"
)

// TrajectoryStep is one broadcast and its concrete hidden-choice outcome.
type TrajectoryStep struct {
	Round        int         `json:"round"`
	Broadcast    Broadcast   `json:"broadcast"`
	SuccessCount int         `json:"success_count"`
	Eligible     []int       `json:"eligible,omitempty"`
	Assignment   []int       `json:"assignment,omitempty"`
	ChosenSets   []SignerSet `json:"chosen_sets,omitempty"`
	Before       Fleet       `json:"before"`
	After        Fleet       `json:"after"`
}

// Trajectory is one fully resolved execution path.
type Trajectory struct {
	Initial Fleet            `json:"initial"`
	Steps   []TrajectoryStep `json:"steps,omitempty"`
	Final   Fleet            `json:"final"`
}

// VerificationReport contains exhaustive acceptance results.
type VerificationReport struct {
	Accepted            bool        `json:"accepted"`
	InitialWorlds       int         `json:"initial_worlds"`
	TrajectoriesChecked int         `json:"trajectories_checked"`
	AssignmentsChecked  int         `json:"assignments_checked"`
	MaxRounds           int         `json:"max_rounds"`
	Violations          []Violation `json:"violations,omitempty"`
}

// Violation is one rejected plan/path and its replay evidence.
type Violation struct {
	Code       string      `json:"code"`
	Message    string      `json:"message"`
	Trajectory *Trajectory `json:"trajectory,omitempty"`
}

// AcceptanceError wraps a failed independent acceptance report.
type AcceptanceError struct {
	Report *VerificationReport
}

func (e *AcceptanceError) Error() string {
	if e == nil || e.Report == nil || len(e.Report.Violations) == 0 {
		return "upgradeplan: acceptance failed"
	}
	v := e.Report.Violations[0]
	if v.Trajectory != nil {
		return fmt.Sprintf("upgradeplan: acceptance failed: %s: %s (see trajectory, %d violation(s))", v.Code, v.Message, len(e.Report.Violations))
	}
	return fmt.Sprintf("upgradeplan: acceptance failed: %s: %s (%d violation(s))", v.Code, v.Message, len(e.Report.Violations))
}

// VerifyPlan independently replays every initial fleet and every eligible
// device choice under the count-only plan. It does not use planner search
// results or memoization.
func VerifyPlan(problem Problem, plan *Plan) (*VerificationReport, error) {
	np, err := NormalizeProblem(problem)
	if err != nil {
		return nil, err
	}
	if plan == nil {
		return nil, fmt.Errorf("upgradeplan: plan is nil")
	}
	if plan.Root == nil {
		return nil, fmt.Errorf("upgradeplan: plan root is nil")
	}

	verifier := &acceptanceVerifier{np: np, report: &VerificationReport{
		InitialWorlds: len(np.belief.Worlds),
		MaxRounds:     np.maxRounds,
	}, visited: make(map[string]struct{})}
	for _, initial := range np.belief.Worlds {
		trajectory := Trajectory{Initial: append(Fleet(nil), initial...)}
		verifier.verifyFleet(initial, plan.Root, nil, trajectory, 0)
	}
	verifier.report.Accepted = len(verifier.report.Violations) == 0
	return verifier.report, nil
}

// Accept is the concise independent acceptance entry point.
func Accept(problem Problem, plan *Plan) (*VerificationReport, error) {
	return VerifyPlan(problem, plan)
}

// AcceptPlan returns an *AcceptanceError when exhaustive verification fails.
func AcceptPlan(problem Problem, plan *Plan) error {
	report, err := VerifyPlan(problem, plan)
	if err != nil {
		return err
	}
	if !report.Accepted {
		return &AcceptanceError{Report: report}
	}
	return nil
}

type acceptanceVerifier struct {
	np      *normalizedProblem
	report  *VerificationReport
	visited map[string]struct{}
}

func (v *acceptanceVerifier) addViolation(code, message string, trajectory Trajectory) {
	path := cloneTrajectory(trajectory)
	v.report.Violations = append(v.report.Violations, Violation{
		Code:       code,
		Message:    message,
		Trajectory: &path,
	})
}

func (v *acceptanceVerifier) verifyFleet(fleet Fleet, node *PlanNode, steps []TrajectoryStep, trajectory Trajectory, usedRounds int) {
	if reason := fleetDataLossReason(fleet); reason != "" {
		trajectory.Steps = append([]TrajectoryStep(nil), steps...)
		trajectory.Final = append(Fleet(nil), fleet...)
		v.addViolation(ReasonDataLost, reason, trajectory)
		return
	}

	if node == nil {
		trajectory.Steps = append([]TrajectoryStep(nil), steps...)
		trajectory.Final = append(Fleet(nil), fleet...)
		v.addViolation("missing_branch", "plan has no node for an observed count", trajectory)
		return
	}

	if node.Terminal {
		trajectory.Steps = append([]TrajectoryStep(nil), steps...)
		trajectory.Final = append(Fleet(nil), fleet...)
		if node.Broadcast != nil || len(node.Branches) > 0 {
			v.addViolation(ReasonInvalidPlan, "terminal node must not contain a broadcast or branches", trajectory)
			return
		}
		if ok, reason := v.np.goalFleet(fleet); !ok {
			v.addViolation("not_trusted", reason, trajectory)
			return
		}
		v.report.TrajectoriesChecked++
		return
	}

	if usedRounds >= v.np.maxRounds {
		trajectory.Steps = append([]TrajectoryStep(nil), steps...)
		trajectory.Final = append(Fleet(nil), fleet...)
		v.addViolation("round_limit", fmt.Sprintf("plan needs more than %d rounds", v.np.maxRounds), trajectory)
		return
	}

	visitKey := fmt.Sprintf("%d|%p|%s", usedRounds, node, fleet.Key())
	if _, seen := v.visited[visitKey]; seen {
		return
	}
	v.visited[visitKey] = struct{}{}

	if node.Broadcast == nil {
		trajectory.Steps = append([]TrajectoryStep(nil), steps...)
		trajectory.Final = append(Fleet(nil), fleet...)
		v.addViolation(ReasonInvalidPlan, "nonterminal node has no broadcast", trajectory)
		return
	}
	release, ok := v.np.byID[node.Broadcast.ReleaseID]
	if !ok {
		trajectory.Steps = append([]TrajectoryStep(nil), steps...)
		trajectory.Final = append(Fleet(nil), fleet...)
		v.addViolation("unknown_release", fmt.Sprintf("broadcast release %q is not in records", node.Broadcast.ReleaseID), trajectory)
		return
	}
	if node.Broadcast.Package != "" && release.Package != "" && node.Broadcast.Package != release.Package {
		trajectory.Steps = append([]TrajectoryStep(nil), steps...)
		trajectory.Final = append(Fleet(nil), fleet...)
		v.addViolation(ReasonInvalidPlan, "broadcast package does not match release record", trajectory)
		return
	}

	outcomes, err := EnumerateOutcomes(fleet, release)
	if err != nil {
		trajectory.Steps = append([]TrajectoryStep(nil), steps...)
		trajectory.Final = append(Fleet(nil), fleet...)
		v.addViolation(ReasonInvalidPlan, err.Error(), trajectory)
		return
	}

	v.report.AssignmentsChecked += len(outcomes)
	type pendingChild struct {
		count      int
		node       *PlanNode
		fleet      Fleet
		steps      []TrajectoryStep
		trajectory Trajectory
	}
	uniqueChildren := make(map[string]pendingChild)

	for _, outcome := range outcomes {
		step := TrajectoryStep{
			Round:        usedRounds + 1,
			Broadcast:    *node.Broadcast,
			SuccessCount: outcome.SuccessCount,
			Eligible:     append([]int(nil), outcome.Eligible...),
			Assignment:   append([]int(nil), outcome.Assignment...),
			ChosenSets:   append([]SignerSet(nil), outcome.ChosenSets...),
			Before:       append(Fleet(nil), fleet...),
			After:        append(Fleet(nil), outcome.Fleet...),
		}
		nextSteps := append(append([]TrajectoryStep(nil), steps...), step)
		nextTrajectory := trajectory
		nextTrajectory.Initial = append(Fleet(nil), trajectory.Initial...)

		if reason := fleetDataLossReason(outcome.Fleet); reason != "" {
			nextTrajectory.Steps = nextSteps
			nextTrajectory.Final = append(Fleet(nil), outcome.Fleet...)
			v.addViolation(ReasonDataLost, reasonAtRound(step.Round, reason), nextTrajectory)
			continue
		}

		child, ok := node.Branches[outcome.SuccessCount]
		if !ok {
			nextTrajectory.Steps = nextSteps
			nextTrajectory.Final = append(Fleet(nil), outcome.Fleet...)
			v.addViolation("missing_count_branch", fmt.Sprintf("no next broadcast for success count %d", outcome.SuccessCount), nextTrajectory)
			continue
		}
		childKey := outcome.Fleet.Key()
		if _, seen := uniqueChildren[childKey]; !seen {
			uniqueChildren[childKey] = pendingChild{
				count:      outcome.SuccessCount,
				node:       child,
				fleet:      outcome.Fleet,
				steps:      nextSteps,
				trajectory: nextTrajectory,
			}
		}
	}

	childKeys := make([]string, 0, len(uniqueChildren))
	for key := range uniqueChildren {
		childKeys = append(childKeys, key)
	}
	sort.Strings(childKeys)
	for _, key := range childKeys {
		child := uniqueChildren[key]
		v.verifyFleet(child.fleet, child.node, child.steps, child.trajectory, usedRounds+1)
	}
}

func fleetDataLossReason(fleet Fleet) string {
	for i, device := range fleet {
		if reason := dataLossReason(device); reason != "" {
			return fmt.Sprintf("round trace device %d: %s", i, reason)
		}
	}
	return ""
}

func reasonAtRound(round int, reason string) string {
	return fmt.Sprintf("round %d: %s", round, reason)
}

func cloneTrajectory(trajectory Trajectory) Trajectory {
	out := Trajectory{
		Initial: append(Fleet(nil), trajectory.Initial...),
		Final:   append(Fleet(nil), trajectory.Final...),
		Steps:   make([]TrajectoryStep, len(trajectory.Steps)),
	}
	for i, step := range trajectory.Steps {
		out.Steps[i] = TrajectoryStep{
			Round:        step.Round,
			Broadcast:    step.Broadcast,
			SuccessCount: step.SuccessCount,
			Eligible:     append([]int(nil), step.Eligible...),
			Assignment:   append([]int(nil), step.Assignment...),
			ChosenSets:   append([]SignerSet(nil), step.ChosenSets...),
			Before:       append(Fleet(nil), step.Before...),
			After:        append(Fleet(nil), step.After...),
		}
	}
	return out
}

// AcceptTrajectory independently checks a resolved trajectory against target.
// Sticky DataLost markers and disappearance of initially present data are
// rejected even when the final signer set is trusted.
func AcceptTrajectory(trajectory Trajectory, target Target) error {
	target.Trusted = target.Trusted.Normalize()
	for i := range target.TrustedSets {
		target.TrustedSets[i] = target.TrustedSets[i].Normalize()
	}

	initial := CanonicalFleet(trajectory.Initial)
	if reason := fleetDataLossReason(initial); reason != "" {
		return fmt.Errorf("upgradeplan: initial trajectory state unacceptable: %s", reason)
	}

	for _, step := range trajectory.Steps {
		after := CanonicalFleet(step.After)
		if reason := fleetDataLossReason(after); reason != "" {
			return fmt.Errorf("upgradeplan: round %d broadcast %q: %s", step.Round, step.Broadcast.ReleaseID, reason)
		}
	}

	final := trajectory.Final
	if len(final) == 0 && len(trajectory.Steps) > 0 {
		final = trajectory.Steps[len(trajectory.Steps)-1].After
	}
	return AcceptFinalState(final, target)
}

// AcceptFinalState independently validates one terminal fleet. Sticky data-loss
// markers or disappearance of initial data reject an otherwise trusted state.
func AcceptFinalState(devices []DeviceState, target Target) error {
	target.Trusted = target.Trusted.Normalize()
	for i := range target.TrustedSets {
		target.TrustedSets[i] = target.TrustedSets[i].Normalize()
	}
	final := CanonicalFleet(devices)
	if reason := fleetDataLossReason(final); reason != "" {
		return fmt.Errorf("upgradeplan: final state rejected: %s", reason)
	}
	for i, device := range final {
		if !target.matches(device.Signers) {
			return fmt.Errorf("upgradeplan: final device %d is not in trusted signer set", i)
		}
	}
	return nil
}

// CalculateWorstRounds independently recomputes the maximum broadcast depth.
func (p *Plan) CalculateWorstRounds() int {
	if p == nil {
		return 0
	}
	return p.Root.CalculateWorstRounds()
}

// CalculateWorstRounds returns the maximum number of broadcasts below node.
func (n *PlanNode) CalculateWorstRounds() int {
	if n == nil || n.Terminal {
		return 0
	}
	worst := 1
	for _, child := range n.Branches {
		if candidate := 1 + child.CalculateWorstRounds(); candidate > worst {
			worst = candidate
		}
	}
	return worst
}

// TreeText renders the count decision tree for golden comparison and debugging.
func (p *Plan) TreeText() string {
	if p == nil || p.Root == nil {
		return "<nil plan>"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "worst_rounds=%d\n", p.WorstRounds)
	writeNodeText(&b, p.Root, "")
	return b.String()
}

func writeNodeText(b *strings.Builder, node *PlanNode, indent string) {
	if node == nil {
		b.WriteString(indent + "<nil>\n")
		return
	}
	if node.Terminal {
		b.WriteString(indent + "terminal\n")
		return
	}
	if node.Broadcast != nil {
		fmt.Fprintf(b, "%sbroadcast %s\n", indent, node.Broadcast.ReleaseID)
	}
	counts := make([]int, 0, len(node.Branches))
	for count := range node.Branches {
		counts = append(counts, count)
	}
	sortInts(counts)
	for _, count := range counts {
		fmt.Fprintf(b, "%scount=%d ->\n", indent+"  ", count)
		writeNodeText(b, node.Branches[count], indent+"    ")
	}
}

func sortInts(values []int) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j-1] > values[j]; j-- {
			values[j-1], values[j] = values[j], values[j-1]
		}
	}
}
