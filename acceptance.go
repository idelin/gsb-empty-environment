package changeaccept

import (
	"errors"
	"fmt"
	"sort"
)

// MaxSwitchGroups is the hard bound on atomic switch groups.
const MaxSwitchGroups = 16

// Outcome is three-valued so an exhausted search can never masquerade as a
// rejection or an approval.
type Outcome uint8

const (
	OutcomeCompliant Outcome = iota
	OutcomeNonCompliant
	OutcomeUndetermined
)

func (o Outcome) String() string {
	switch o {
	case OutcomeCompliant:
		return "compliant"
	case OutcomeNonCompliant:
		return "non-compliant"
	case OutcomeUndetermined:
		return "undetermined"
	default:
		return "unknown"
	}
}

// PolicyChange replaces one plane at one endpoint. Old is optional; when set it
// must equal the base rule and provides an explicit old/new pairing.
type PolicyChange struct {
	Endpoint EndpointID
	Plane    Plane
	Old      Policy
	New      Policy
}

// SwitchGroup is one atomic multi-plane policy update. Changes within a group
// are applied together. DependsOn names groups that must precede this group.
type SwitchGroup struct {
	ID        GroupID
	DependsOn []GroupID
	Changes   []PolicyChange
}

// Input is a complete acceptance problem. Topology carries the old rules.
// Order is the proposed atomic-group order; nil means input group order.
type Input struct {
	BaseGeneration Generation
	Topology       Topology
	Auth           *Authorizer
	Groups         []SwitchGroup
	Order          []GroupID
	Pairs          []TenantPair
	// Labels sets the exact initial-label domain when non-empty. If empty, the
	// domain is inferred from endpoint rules, label graphs, and authorizer rules.
	// Concrete probe and active-connection labels are always included.
	Labels []Label
	Probes []Probe
	Active []ActiveConnection
	Budget Budget
}

// ProbeResult reports a named liveness path at one prefix.
type ProbeResult struct {
	Probe   Probe
	Outcome Outcome
	Path    Path
	Error   string
}

// ActiveResult reports an established connection at one prefix.
type ActiveResult struct {
	ConnectionID string
	Outcome      Outcome
	Violations   []Violation
}

// PrefixReport is one candidate-order prefix, including the empty prefix.
type PrefixReport struct {
	Applied         []GroupID
	Generation      Generation
	Outcome         Outcome
	Violations      []Violation
	Probes          []ProbeResult
	Active          []ActiveResult
	States          int
	Transitions     int
	BudgetExhausted bool
}

// OrderReport is the result of checking one candidate order at every prefix.
type OrderReport struct {
	Order    []GroupID
	Prefixes []PrefixReport
	Outcome  Outcome
}

// ScheduleReport is the result of the minimum-adjacent-swap reorder search.
type ScheduleReport struct {
	Order    []GroupID
	Swaps    int
	Prefixes []PrefixReport
	Outcome  Outcome
}

// AcceptanceReport keeps the original verdict separate from the post-reorder
// verdict. OriginalOutcome is never rewritten.
type AcceptanceReport struct {
	Given       OrderReport
	Recommended *ScheduleReport
	Outcome     Outcome
}

// Validate checks input structure, managed-tenant closure, old/new policy
// consistency, group disjointness, probes, and active-connection histories.
func (in Input) Validate() error {
	compiled, err := in.compile()
	if err != nil {
		return err
	}
	return checkOrder(compiled.order, compiled.groups)
}

type compiledInput struct {
	groups map[GroupID]SwitchGroup
	order  []GroupID
	pairs  []TenantPair
	extras []Label
}

func (in Input) compile() (compiledInput, error) {
	if err := in.Topology.Validate(); err != nil {
		return compiledInput{}, err
	}
	if in.Auth == nil {
		return compiledInput{}, fmt.Errorf("input requires an authorizer")
	}
	endpointMap := in.Topology.endpointMap()
	for _, endpoint := range in.Topology.Endpoints {
		if !in.Auth.IsManaged(endpoint.Tenant) {
			return compiledInput{}, fmt.Errorf("endpoint %q uses unmanaged tenant %q", endpoint.ID, endpoint.Tenant)
		}
	}
	if len(in.Groups) > MaxSwitchGroups {
		return compiledInput{}, fmt.Errorf("got %d switch groups, maximum is %d", len(in.Groups), MaxSwitchGroups)
	}

	groups := make(map[GroupID]SwitchGroup, len(in.Groups))
	targetOwner := make(map[string]GroupID)
	for _, group := range in.Groups {
		if _, exists := groups[group.ID]; exists {
			return compiledInput{}, fmt.Errorf("switch group %d is duplicated", group.ID)
		}
		if len(group.Changes) == 0 {
			return compiledInput{}, fmt.Errorf("switch group %d has no changes", group.ID)
		}
		copyGroup := SwitchGroup{
			ID:        group.ID,
			DependsOn: append([]GroupID(nil), group.DependsOn...),
			Changes:   append([]PolicyChange(nil), group.Changes...),
		}
		for changeIndex, change := range copyGroup.Changes {
			endpoint, ok := endpointMap[change.Endpoint]
			if !ok {
				return compiledInput{}, fmt.Errorf("group %d change %d: unknown endpoint %q", group.ID, changeIndex, change.Endpoint)
			}
			if !change.Plane.valid() {
				return compiledInput{}, fmt.Errorf("group %d change %d: invalid plane %d", group.ID, changeIndex, change.Plane)
			}
			if err := change.New.validate(fmt.Sprintf("group %d change %d new policy", group.ID, changeIndex)); err != nil {
				return compiledInput{}, err
			}
			if change.Old != nil {
				if err := change.Old.validate(fmt.Sprintf("group %d change %d old policy", group.ID, changeIndex)); err != nil {
					return compiledInput{}, err
				}
				if !equalPolicy(change.Old, endpoint.policy(change.Plane)) {
					return compiledInput{}, fmt.Errorf("group %d change %d: old %s policy at %q does not match topology", group.ID, changeIndex, change.Plane, change.Endpoint)
				}
			}
			target := fmt.Sprintf("%s\x00%d", change.Endpoint, change.Plane)
			if owner, exists := targetOwner[target]; exists {
				return compiledInput{}, fmt.Errorf("groups %d and %d both replace %s policy at %q", owner, group.ID, change.Plane, change.Endpoint)
			}
			targetOwner[target] = group.ID
		}
		groups[group.ID] = copyGroup
	}
	for _, group := range in.Groups {
		for _, dependency := range group.DependsOn {
			if dependency == group.ID {
				return compiledInput{}, fmt.Errorf("group %d depends on itself", group.ID)
			}
			if _, ok := groups[dependency]; !ok {
				return compiledInput{}, fmt.Errorf("group %d depends on unknown group %d", group.ID, dependency)
			}
		}
	}
	if dependencyCycle(groups) {
		return compiledInput{}, fmt.Errorf("switch-group dependencies contain a cycle")
	}

	order := in.resolvedOrder()

	pairs := append([]TenantPair(nil), in.Pairs...)
	if len(pairs) == 0 {
		pairs = in.Auth.AllManagedPairs()
	}
	seenPairs := make(map[TenantPair]struct{}, len(pairs))
	extras := make(map[Label]struct{})
	for _, label := range in.Labels {
		extras[label] = struct{}{}
	}
	for _, pair := range pairs {
		if !in.Auth.IsManaged(pair.Source) || !in.Auth.IsManaged(pair.Destination) {
			return compiledInput{}, fmt.Errorf("verification pair %q -> %q is not fully managed", pair.Source, pair.Destination)
		}
		if _, exists := seenPairs[pair]; exists {
			return compiledInput{}, fmt.Errorf("verification pair %q -> %q is duplicated", pair.Source, pair.Destination)
		}
		seenPairs[pair] = struct{}{}
	}
	for index, probe := range in.Probes {
		probe, err := resolveProbe(probe, endpointMap)
		if err != nil {
			return compiledInput{}, fmt.Errorf("probe %d: %w", index, err)
		}
		if !in.Auth.IsManaged(probe.Pair.Source) || !in.Auth.IsManaged(probe.Pair.Destination) {
			return compiledInput{}, fmt.Errorf("probe %q: pair is not fully managed", probe.ID)
		}
		if probe.InitialLabel != "" {
			extras[probe.InitialLabel] = struct{}{}
		}
	}
	for index, active := range in.Active {
		if len(active.Path) < 2 || len(active.Path) != len(active.Labels) {
			return compiledInput{}, fmt.Errorf("active connection %d: path and labels must have equal length >= 2", index)
		}
		source, ok := endpointMap[active.Path[0]]
		if !ok {
			return compiledInput{}, fmt.Errorf("active connection %d: unknown source %q", index, active.Path[0])
		}
		destination, ok := endpointMap[active.Path[len(active.Path)-1]]
		if !ok {
			return compiledInput{}, fmt.Errorf("active connection %d: unknown destination %q", index, active.Path[len(active.Path)-1])
		}
		if source.Tenant != active.Pair.Source || destination.Tenant != active.Pair.Destination {
			return compiledInput{}, fmt.Errorf("active connection %d: endpoint tenants do not match pair", index)
		}
		if active.VerifiedThrough < 0 || active.VerifiedThrough > len(active.Path)-1 {
			return compiledInput{}, fmt.Errorf("active connection %d: VerifiedThrough is out of range", index)
		}
		if active.VerifiedThrough == 0 {
			if active.VerifiedHistory != ([32]byte{}) {
				return compiledInput{}, fmt.Errorf("active connection %d: VerifiedHistory requires a non-zero VerifiedThrough", index)
			}
		} else {
			want := HistoryDigest(active.Path[:active.VerifiedThrough+1], active.Labels[:active.VerifiedThrough+1])
			if active.VerifiedHistory != want {
				return compiledInput{}, fmt.Errorf("active connection %d: VerifiedHistory does not match its path/label prefix", index)
			}
		}
		for _, label := range active.Labels {
			extras[label] = struct{}{}
		}
	}

	return compiledInput{
		groups: groups,
		order:  append([]GroupID(nil), order...),
		pairs:  pairs,
		extras: sortedLabelSet(extras),
	}, nil
}

func (in Input) resolvedOrder() []GroupID {
	if in.Order != nil {
		return append([]GroupID(nil), in.Order...)
	}
	result := make([]GroupID, 0, len(in.Groups))
	for _, group := range in.Groups {
		result = append(result, group.ID)
	}
	return result
}

func resolveProbe(probe Probe, endpoints map[EndpointID]Endpoint) (Probe, error) {
	source, ok := endpoints[probe.Source]
	if !ok {
		return Probe{}, fmt.Errorf("unknown source %q", probe.Source)
	}
	destination, ok := endpoints[probe.Destination]
	if !ok {
		return Probe{}, fmt.Errorf("unknown destination %q", probe.Destination)
	}
	if probe.Pair.Source == "" {
		probe.Pair.Source = source.Tenant
	}
	if probe.Pair.Destination == "" {
		probe.Pair.Destination = destination.Tenant
	}
	if source.Tenant != probe.Pair.Source || destination.Tenant != probe.Pair.Destination {
		return Probe{}, fmt.Errorf("endpoint tenants do not match probe pair")
	}
	return probe, nil
}

func dependencyCycle(groups map[GroupID]SwitchGroup) bool {
	state := make(map[GroupID]uint8, len(groups))
	var visit func(GroupID) bool
	visit = func(id GroupID) bool {
		switch state[id] {
		case 1:
			return true
		case 2:
			return false
		}
		state[id] = 1
		for _, dependency := range groups[id].DependsOn {
			if visit(dependency) {
				return true
			}
		}
		state[id] = 2
		return false
	}
	ids := make([]GroupID, 0, len(groups))
	for id := range groups {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		if visit(id) {
			return true
		}
	}
	return false
}

// CheckOrder verifies every prefix of the supplied order. A nil order uses the
// input's candidate order.
func (in Input) CheckOrder(order []GroupID) (OrderReport, error) {
	compiled, err := in.compile()
	if err != nil {
		return OrderReport{}, err
	}
	if order == nil {
		order = compiled.order
	}
	if err := checkOrder(order, compiled.groups); err != nil {
		return OrderReport{}, err
	}
	return in.checkResolvedOrder(order, compiled)
}

// Check is CheckOrder(nil).
func (in Input) Check() (OrderReport, error) {
	return in.CheckOrder(nil)
}

// BuildSnapshot materializes a complete policy snapshot for order. A nil order
// uses the input's candidate order. The returned snapshot is suitable for
// BuildCoverageCertificate and CheckCoverageCertificate.
func (in Input) BuildSnapshot(order []GroupID) (Snapshot, error) {
	compiled, err := in.compile()
	if err != nil {
		return Snapshot{}, err
	}
	if order == nil {
		order = compiled.order
	}
	if err := checkOrder(order, compiled.groups); err != nil {
		return Snapshot{}, err
	}
	snapshot, err := ApplyGroups(in.Topology, in.BaseGeneration, compiled.groups, order)
	if err != nil {
		return Snapshot{}, err
	}
	snapshot.Auth = in.Auth
	snapshot.Labels = append([]Label(nil), in.Labels...)
	return snapshot, nil
}

func (in Input) checkResolvedOrder(order []GroupID, compiled compiledInput) (OrderReport, error) {
	report := OrderReport{Order: append([]GroupID(nil), order...)}
	baseGeneration := in.BaseGeneration
	for prefix := 0; prefix <= len(order); prefix++ {
		snapshot, err := ApplyGroups(in.Topology, baseGeneration, compiled.groups, order[:prefix])
		if err != nil {
			return OrderReport{}, err
		}
		snapshot.Auth = in.Auth
		snapshot.Labels = append([]Label(nil), in.Labels...)
		prefixReport := in.verifySnapshot(snapshot, compiled)
		prefixReport.Applied = append([]GroupID(nil), order[:prefix]...)
		report.Prefixes = append(report.Prefixes, prefixReport)
	}
	report.Outcome = combineOutcomes(report.Prefixes)
	return report, nil
}

func checkOrder(order []GroupID, groups map[GroupID]SwitchGroup) error {
	if err := checkOrderSet(order, groups); err != nil {
		return err
	}
	position := make(map[GroupID]int, len(order))
	for index, id := range order {
		position[id] = index
	}
	for index, id := range order {
		for _, dependency := range groups[id].DependsOn {
			if position[dependency] >= index {
				return &OrderDependencyError{Group: id, Dependency: dependency}
			}
		}
	}
	return nil
}

func checkOrderSet(order []GroupID, groups map[GroupID]SwitchGroup) error {
	if len(order) != len(groups) {
		return fmt.Errorf("order has %d entries for %d groups", len(order), len(groups))
	}
	seen := make(map[GroupID]struct{}, len(order))
	for _, id := range order {
		if _, ok := groups[id]; !ok {
			return fmt.Errorf("order references unknown group %d", id)
		}
		if _, exists := seen[id]; exists {
			return fmt.Errorf("order repeats group %d", id)
		}
		seen[id] = struct{}{}
	}
	return nil
}

// OrderDependencyError reports an inadmissible candidate order. The order can
// still be repaired by FindMinimalOrder or Accept.
type OrderDependencyError struct {
	Group      GroupID
	Dependency GroupID
}

func (e *OrderDependencyError) Error() string {
	return fmt.Sprintf("group %d appears before its dependency %d", e.Group, e.Dependency)
}

func (in Input) verifySnapshot(snapshot Snapshot, compiled compiledInput) PrefixReport {
	budget := newMeter(in.Budget)
	report := PrefixReport{Generation: snapshot.Generation}
	labels := snapshotLabels(snapshot, compiled.extras)
	budgetError := false
	for _, pair := range compiled.pairs {
		exploration, err := explorePair(snapshot, pair, labels, budget)
		report.Violations = append(report.Violations, exploration.Violations...)
		if err != nil {
			if err == ErrBudgetExhausted {
				budgetError = true
				break
			}
			report.Violations = append(report.Violations, Violation{
				Kind:   ViolationConnectionMalformed,
				Pair:   pair,
				Detail: err.Error(),
			})
		}
	}
	if !budgetError {
		for _, rawProbe := range in.Probes {
			probe, _ := resolveProbe(rawProbe, snapshot.Topology.endpointMap())
			path, feasible, err := findProbePath(snapshot, probe, labels, budget)
			result := ProbeResult{Probe: probe, Outcome: OutcomeUndetermined}
			if err != nil {
				budgetError = true
				result.Error = err.Error()
				report.Probes = append(report.Probes, result)
				break
			}
			if feasible {
				result.Outcome = OutcomeCompliant
				result.Path = path
			} else {
				result.Outcome = OutcomeNonCompliant
				result.Error = "no authorized policy-valid path"
				report.Violations = append(report.Violations, Violation{
					Kind:   ViolationLiveness,
					Pair:   probe.Pair,
					Detail: fmt.Sprintf("probe %q has no feasible path from %q to %q", probe.ID, probe.Source, probe.Destination),
				})
			}
			report.Probes = append(report.Probes, result)
		}
	}
	for _, active := range in.Active {
		violations := verifyConnection(active.Connection, snapshot, active.VerifiedThrough)
		result := ActiveResult{
			ConnectionID: active.ID,
			Outcome:      OutcomeCompliant,
			Violations:   violations,
		}
		if len(violations) > 0 {
			result.Outcome = OutcomeNonCompliant
			report.Violations = append(report.Violations, violations...)
		}
		report.Active = append(report.Active, result)
	}
	report.States = budget.states
	report.Transitions = budget.transitions
	report.BudgetExhausted = budgetError
	switch {
	case len(report.Violations) > 0:
		report.Outcome = OutcomeNonCompliant
	case budgetError:
		report.Outcome = OutcomeUndetermined
	default:
		report.Outcome = OutcomeCompliant
	}
	return report
}

func combineOutcomes(prefixes []PrefixReport) Outcome {
	result := OutcomeCompliant
	for _, prefix := range prefixes {
		if prefix.Outcome == OutcomeNonCompliant {
			return OutcomeNonCompliant
		}
		if prefix.Outcome == OutcomeUndetermined {
			result = OutcomeUndetermined
		}
	}
	return result
}

// Accept checks the given order and, only when it is definitely non-compliant,
// searches dependency-respecting reorderings for the minimum adjacent-swap fix.
func (in Input) Accept() (AcceptanceReport, error) {
	given, err := in.Check()
	if err != nil {
		var dependencyError *OrderDependencyError
		if errors.As(err, &dependencyError) {
			recommended, reorderErr := in.FindMinimalOrder()
			if reorderErr != nil {
				return AcceptanceReport{}, reorderErr
			}
			return AcceptanceReport{
				Given:       OrderReport{Order: in.resolvedOrder(), Outcome: OutcomeNonCompliant},
				Recommended: &recommended,
				Outcome:     recommended.Outcome,
			}, nil
		}
		return AcceptanceReport{}, err
	}
	result := AcceptanceReport{Given: given, Outcome: given.Outcome}
	if given.Outcome != OutcomeNonCompliant {
		return result, nil
	}
	recommended, err := in.FindMinimalOrder()
	if err != nil {
		return AcceptanceReport{}, err
	}
	result.Recommended = &recommended
	result.Outcome = recommended.Outcome
	return result, nil
}
