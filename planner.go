package iac

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
)

// PlanOptions controls target closure and destroy mode.
type PlanOptions struct {
	Targets []string `json:"targets,omitempty"`
	Destroy bool     `json:"destroy,omitempty"`
}

// ResourcePlan is the final decision for one resource.
type ResourcePlan struct {
	Address             string       `json:"address"`
	Action              ActionKind   `json:"action"`
	Proposed            ActionKind   `json:"proposed,omitempty"`
	Reason              ChangeReason `json:"reason,omitempty"`
	CreateBeforeDestroy bool         `json:"create_before_destroy,omitempty"`
	ForbidCBD           bool         `json:"forbid_cbd,omitempty"`
	PreventDestroy      bool         `json:"prevent_destroy,omitempty"`
	TriggerChain        []string     `json:"trigger_chain,omitempty"`
	Drift               []string     `json:"drift,omitempty"`
}

// Wave is one maximal-concurrency execution wave.
type Wave struct {
	Index   int      `json:"index"`
	NodeIDs []string `json:"node_ids"`
}

// Plan is a compiled, executable plan.
type Plan struct {
	Destroy   bool                  `json:"destroy,omitempty"`
	Targets   []string              `json:"targets,omitempty"`
	Resources []ResourcePlan        `json:"resources"`
	Actions   map[string]ActionKind `json:"actions"`
	Graph     *Graph                `json:"graph"`
	Waves     []Wave                `json:"waves,omitempty"`
}

// BuildPlan runs drift detection, replace_triggered_by cascading, lifecycle
// propagation, safety validation and action-graph compilation.
func BuildPlan(resources []Resource, opts ...PlanOptions) (*Plan, error) {
	var options PlanOptions
	if len(opts) > 0 {
		options = opts[0]
	}
	return newPlanner(resources, options).build()
}

// PlanResources is an alias for BuildPlan.
func PlanResources(resources []Resource, opts ...PlanOptions) (*Plan, error) {
	return BuildPlan(resources, opts...)
}

type decision struct {
	resource     Resource
	proposed     ActionKind
	action       ActionKind
	reason       ChangeReason
	drift        []string
	triggerChain []string
}

type planner struct {
	all           []Resource
	byAddr        map[string]*Resource
	order         []string
	deps          map[string][]string
	dependents    map[string][]string
	selected      map[string]bool
	selectedOrder []string
	opts          PlanOptions
}

func newPlanner(resources []Resource, opts PlanOptions) *planner {
	p := &planner{
		all:        resources,
		byAddr:     make(map[string]*Resource, len(resources)),
		deps:       make(map[string][]string, len(resources)),
		dependents: make(map[string][]string, len(resources)),
		selected:   make(map[string]bool, len(resources)),
		opts:       opts,
	}
	return p
}

func (p *planner) build() (*Plan, error) {
	if err := p.indexResources(); err != nil {
		return nil, err
	}
	if err := p.validateReferences(); err != nil {
		return nil, err
	}
	if cycle := findAddressCycle(p.order, p.deps); len(cycle) > 0 {
		return nil, dependencyCycleError(cycle)
	}
	if err := p.selectTargets(); err != nil {
		return nil, err
	}

	decisions := make(map[string]*decision, len(p.selectedOrder))
	for _, addr := range p.selectedOrder {
		d, err := p.resolveInitial(addr)
		if err != nil {
			return nil, err
		}
		decisions[addr] = d
	}

	if !p.opts.Destroy {
		p.cascadeReplaceTriggers(decisions)
	}
	cbd, conflicts := p.propagateCBD(decisions)
	if len(conflicts) > 0 {
		return nil, cbdConflictError(conflicts)
	}
	for _, d := range decisions {
		d.action = lowerReplace(d.action, cbd[d.resource.Address])
	}

	if violations := p.preventDestroyViolations(decisions); len(violations) > 0 {
		return nil, preventDestroyError(violations)
	}

	graph := p.compileGraph(decisions)
	if cycle := graph.FindMinimalCycle(); len(cycle) > 0 {
		return nil, executionCycleError(cycle)
	}
	waves, err := graph.TopologicalWaves()
	if err != nil {
		return nil, err
	}

	plan := &Plan{
		Destroy: p.opts.Destroy,
		Targets: append([]string(nil), p.opts.Targets...),
		Actions: make(map[string]ActionKind, len(decisions)),
		Graph:   graph,
	}
	for _, addr := range p.selectedOrder {
		d := decisions[addr]
		plan.Actions[addr] = d.action
		plan.Resources = append(plan.Resources, ResourcePlan{
			Address:             addr,
			Action:              d.action,
			Proposed:            d.proposed,
			Reason:              d.reason,
			CreateBeforeDestroy: cbd[addr] && d.action.IsReplace(),
			ForbidCBD:           d.resource.Lifecycle.ForbidCBD,
			PreventDestroy:      d.resource.Lifecycle.PreventDestroy,
			TriggerChain:        append([]string(nil), d.triggerChain...),
			Drift:               append([]string(nil), d.drift...),
		})
	}
	sort.Slice(plan.Resources, func(i, j int) bool {
		return plan.Resources[i].Address < plan.Resources[j].Address
	})
	for i, ids := range waves {
		plan.Waves = append(plan.Waves, Wave{Index: i + 1, NodeIDs: append([]string(nil), ids...)})
	}
	return plan, nil
}

func (p *planner) indexResources() error {
	for i := range p.all {
		r := &p.all[i]
		if strings.TrimSpace(r.Address) == "" {
			return &PlanError{Code: ErrorInvalidAction, Message: "resource address must not be empty"}
		}
		if _, ok := p.byAddr[r.Address]; ok {
			return &PlanError{Code: ErrorInvalidAction, Message: fmt.Sprintf("duplicate resource address %q", r.Address)}
		}
		p.byAddr[r.Address] = r
		p.order = append(p.order, r.Address)
	}
	sort.Strings(p.order)
	for _, addr := range p.order {
		r := p.byAddr[addr]
		deps := make(map[string]bool)
		for _, dep := range r.DependsOn {
			if !deps[dep] {
				p.deps[addr] = append(p.deps[addr], dep)
				deps[dep] = true
			}
		}
		sort.Strings(p.deps[addr])
	}
	for _, addr := range p.order {
		for _, dep := range p.deps[addr] {
			p.dependents[dep] = append(p.dependents[dep], addr)
		}
	}
	for dep := range p.dependents {
		sort.Strings(p.dependents[dep])
	}
	return nil
}

func (p *planner) validateReferences() error {
	for _, addr := range p.order {
		r := p.byAddr[addr]
		for _, dep := range r.DependsOn {
			if p.byAddr[dep] == nil {
				return &PlanError{
					Code:    ErrorUnknownResource,
					Message: fmt.Sprintf("%s depends on unknown resource %q", addr, dep),
					Chain:   []string{addr, dep},
				}
			}
		}
		for _, trigger := range r.ReplaceTriggeredBy {
			if p.byAddr[trigger] == nil {
				return &PlanError{
					Code:    ErrorUnknownResource,
					Message: fmt.Sprintf("%s has replace_triggered_by reference to unknown resource %q", addr, trigger),
					Chain:   []string{trigger, addr},
				}
			}
		}
	}
	return nil
}

func (p *planner) selectTargets() error {
	if len(p.opts.Targets) == 0 {
		p.selectedOrder = append([]string(nil), p.order...)
		for _, addr := range p.selectedOrder {
			p.selected[addr] = true
		}
		return nil
	}

	selected := make(map[string]bool)
	for _, target := range p.opts.Targets {
		if p.byAddr[target] == nil {
			return &PlanError{Code: ErrorUnknownResource, Message: fmt.Sprintf("unknown target %q", target)}
		}
		if p.opts.Destroy {
			p.walk(target, p.dependents, selected)
		} else {
			p.walk(target, p.deps, selected)
		}
	}
	for _, addr := range p.order {
		if selected[addr] {
			p.selectedOrder = append(p.selectedOrder, addr)
			p.selected[addr] = true
		}
	}
	return nil
}

func (p *planner) walk(start string, adj map[string][]string, seen map[string]bool) {
	if seen[start] {
		return
	}
	seen[start] = true
	for _, next := range adj[start] {
		p.walk(next, adj, seen)
	}
}

func (p *planner) resolveInitial(addr string) (*decision, error) {
	r := p.byAddr[addr]
	d := &decision{resource: *r}

	if p.opts.Destroy {
		d.proposed = ActionDestroy
		d.action = ActionDestroy
		d.reason = ReasonTargetDestroy
		d.triggerChain = []string{addr}
		return d, nil
	}

	rawAction := r.Change.Action
	if rawAction == "" {
		rawAction = r.Action
	}
	forceNew := r.Change.ForceNew || r.ForceNew

	if rawAction != "" {
		action, ok := ParseAction(string(rawAction))
		if !ok {
			return nil, &PlanError{
				Code:    ErrorInvalidAction,
				Message: fmt.Sprintf("%s has invalid action %q", addr, rawAction),
			}
		}
		d.action = action
		d.reason = ReasonConfigured
		if forceNew {
			d.action = upgradeToReplace(d.action)
			d.reason = ReasonForceNew
			d.triggerChain = []string{addr}
		}
		d.proposed = d.action
		return d, nil
	}

	if r.Remote != nil {
		if !r.Remote.Exists {
			d.proposed = ActionCreate
			d.action = ActionCreate
			d.reason = ReasonRemoteMissing
			return d, nil
		}
		drift := diffAttributes(r.Desired, r.Remote.Attributes)
		d.drift = drift
		if len(drift) == 0 {
			d.proposed = ActionNoOp
			d.action = ActionNoOp
			d.reason = ReasonNoChange
			return d, nil
		}
		if intersects(drift, r.ForceNewAttributes) {
			d.proposed = ActionReplace
			d.action = ActionReplace
			d.reason = ReasonForceNew
			d.triggerChain = []string{addr}
			return d, nil
		}
		d.proposed = ActionUpdateInPlace
		d.action = ActionUpdateInPlace
		d.reason = ReasonRemoteDrift
		return d, nil
	}

	if forceNew {
		d.proposed = ActionReplace
		d.action = ActionReplace
		d.reason = ReasonForceNew
		d.triggerChain = []string{addr}
		return d, nil
	}
	d.proposed = ActionNoOp
	d.action = ActionNoOp
	d.reason = ReasonNoChange
	return d, nil
}

func upgradeToReplace(a ActionKind) ActionKind {
	switch a {
	case ActionNoOp, ActionUpdateInPlace, ActionReplace:
		return ActionReplace
	default:
		return a
	}
}

func (p *planner) cascadeReplaceTriggers(decisions map[string]*decision) {
	for {
		changed := false
		for _, addr := range p.selectedOrder {
			d := decisions[addr]
			if d.action == ActionCreate || d.action == ActionDestroy ||
				d.action == ActionCreateThenDestroy || d.action == ActionDestroyThenCreate {
				continue
			}
			triggers := append([]string(nil), d.resource.ReplaceTriggeredBy...)
			sort.Strings(triggers)
			for _, trigger := range triggers {
				src := decisions[trigger]
				if src == nil {
					continue
				}
				if !(src.action == ActionUpdateInPlace || src.action.IsReplace()) {
					continue
				}
				if d.action != ActionReplace {
					d.action = ActionReplace
					d.proposed = ActionReplace
					d.reason = ReasonTriggeredReplace
					changed = true
				}
				chain := sourceChain(src)
				if !containsString(chain, addr) {
					chain = append(chain, addr)
					d.triggerChain = chain
				}
				break
			}
		}
		if !changed {
			return
		}
	}
}

func sourceChain(d *decision) []string {
	if len(d.triggerChain) > 0 {
		return append([]string(nil), d.triggerChain...)
	}
	return []string{d.resource.Address}
}

// propagateCBD returns effective CBD flags and all conflict chains. A
// replacement that is itself create-before-destroy propagates CBD upstream to
// every replacement ancestor, including through non-replaced intermediate
// nodes.
func (p *planner) propagateCBD(decisions map[string]*decision) (map[string]bool, [][]string) {
	cbd := make(map[string]bool, len(decisions))
	for _, addr := range p.selectedOrder {
		d := decisions[addr]
		cbd[addr] = d.resource.Lifecycle.CreateBeforeDestroy || d.action == ActionCreateThenDestroy
	}

	type item struct {
		addr string
		path []string
	}
	queue := make([]item, 0)
	seen := make(map[string]bool)
	var conflicts [][]string
	for _, addr := range p.selectedOrder {
		if !cbd[addr] || !decisions[addr].action.IsReplace() {
			continue
		}
		queue = append(queue, item{addr: addr, path: []string{addr}})
		seen[addr] = true
		if decisions[addr].action.IsReplace() && decisions[addr].resource.Lifecycle.ForbidCBD {
			conflicts = append(conflicts, []string{addr})
		}
	}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, dep := range p.deps[cur.addr] {
			if !p.selected[dep] {
				continue
			}
			path := append(append([]string(nil), cur.path...), dep)
			depDecision := decisions[dep]
			if depDecision.action.IsReplace() {
				if depDecision.resource.Lifecycle.ForbidCBD {
					conflicts = append(conflicts, path)
				} else {
					cbd[dep] = true
				}
			}
			if !seen[dep] {
				seen[dep] = true
				queue = append(queue, item{addr: dep, path: path})
			}
		}
	}
	return cbd, conflicts
}

func lowerReplace(a ActionKind, cbd bool) ActionKind {
	switch a {
	case ActionReplace:
		if cbd {
			return ActionCreateThenDestroy
		}
		return ActionDestroyThenCreate
	case ActionDestroyThenCreate:
		if cbd {
			return ActionCreateThenDestroy
		}
		return ActionDestroyThenCreate
	case ActionCreateThenDestroy:
		return ActionCreateThenDestroy
	default:
		return a
	}
}

func (p *planner) preventDestroyViolations(decisions map[string]*decision) []Violation {
	var violations []Violation
	for _, addr := range p.selectedOrder {
		d := decisions[addr]
		if !d.resource.Lifecycle.PreventDestroy || !d.action.DestroysExisting() {
			continue
		}
		chain := append([]string(nil), d.triggerChain...)
		if len(chain) == 0 {
			chain = []string{addr}
		}
		violations = append(violations, Violation{
			Address: addr,
			Action:  d.action,
			Chain:   chain,
		})
	}
	sort.Slice(violations, func(i, j int) bool {
		return violations[i].Address < violations[j].Address
	})
	return violations
}

func (p *planner) compileGraph(decisions map[string]*decision) *Graph {
	g := NewGraph()
	createID := make(map[string]string)
	updateID := make(map[string]string)
	destroyID := make(map[string]string)
	newID := make(map[string]string)

	for _, addr := range p.selectedOrder {
		action := decisions[addr].action
		switch action {
		case ActionCreate:
			id := "create:" + addr
			createID[addr], newID[addr] = id, id
			_ = g.AddNode(&Node{ID: id, Address: addr, Kind: NodeCreate, Action: ActionCreate})
		case ActionUpdateInPlace:
			id := "update:" + addr
			updateID[addr], newID[addr] = id, id
			_ = g.AddNode(&Node{ID: id, Address: addr, Kind: NodeUpdate, Action: ActionUpdateInPlace})
		case ActionDestroy:
			id := "destroy:" + addr
			destroyID[addr] = id
			_ = g.AddNode(&Node{ID: id, Address: addr, Kind: NodeDestroy, Action: ActionDestroy})
		case ActionCreateThenDestroy:
			createID[addr] = "create:" + addr
			destroyID[addr] = "destroy:" + addr
			newID[addr] = createID[addr]
			_ = g.AddNode(&Node{ID: createID[addr], Address: addr, Kind: NodeCreate, Action: ActionCreate})
			_ = g.AddNode(&Node{ID: destroyID[addr], Address: addr, Kind: NodeDestroy, Action: ActionDestroy})
			_ = g.AddEdge(createID[addr], destroyID[addr])
		case ActionDestroyThenCreate:
			createID[addr] = "create:" + addr
			destroyID[addr] = "destroy:" + addr
			newID[addr] = createID[addr]
			_ = g.AddNode(&Node{ID: createID[addr], Address: addr, Kind: NodeCreate, Action: ActionCreate})
			_ = g.AddNode(&Node{ID: destroyID[addr], Address: addr, Kind: NodeDestroy, Action: ActionDestroy})
			_ = g.AddEdge(destroyID[addr], createID[addr])
		}
	}

	for _, addr := range p.selectedOrder {
		for _, dep := range p.deps[addr] {
			if !p.selected[dep] {
				continue
			}
			if newID[dep] != "" && newID[addr] != "" {
				_ = g.AddEdge(newID[dep], newID[addr])
			}
			if destroyID[dep] != "" && destroyID[addr] != "" {
				_ = g.AddEdge(destroyID[addr], destroyID[dep])
			}
			// With CBD, the old upstream object may only be destroyed after
			// every dependent has moved to the new upstream object.
			if decisions[dep].action == ActionCreateThenDestroy && newID[addr] != "" {
				_ = g.AddEdge(newID[addr], destroyID[dep])
			}
		}
	}
	return g
}

// UpstreamClosure returns targets plus all transitive DependsOn resources.
func UpstreamClosure(resources []Resource, targets ...string) ([]string, error) {
	return targetClosure(resources, targets, false)
}

// DownstreamClosure returns targets plus all transitive dependents.
func DownstreamClosure(resources []Resource, targets ...string) ([]string, error) {
	return targetClosure(resources, targets, true)
}

func targetClosure(resources []Resource, targets []string, downstream bool) ([]string, error) {
	byAddr := make(map[string]bool, len(resources))
	deps := make(map[string][]string, len(resources))
	dependents := make(map[string][]string, len(resources))
	for _, r := range resources {
		byAddr[r.Address] = true
	}
	for _, r := range resources {
		for _, dep := range r.DependsOn {
			if !byAddr[dep] {
				return nil, &PlanError{
					Code:    ErrorUnknownResource,
					Message: fmt.Sprintf("%s depends on unknown resource %q", r.Address, dep),
					Chain:   []string{r.Address, dep},
				}
			}
			deps[r.Address] = append(deps[r.Address], dep)
			dependents[dep] = append(dependents[dep], r.Address)
		}
	}
	selected := make(map[string]bool)
	adj := deps
	if downstream {
		adj = dependents
	}
	var walk func(string)
	walk = func(addr string) {
		if selected[addr] {
			return
		}
		selected[addr] = true
		for _, next := range adj[addr] {
			walk(next)
		}
	}
	for _, target := range targets {
		if !byAddr[target] {
			return nil, &PlanError{Code: ErrorUnknownResource, Message: fmt.Sprintf("unknown target %q", target)}
		}
		walk(target)
	}
	out := make([]string, 0, len(selected))
	for _, r := range resources {
		if selected[r.Address] {
			out = append(out, r.Address)
		}
	}
	sort.Strings(out)
	return out, nil
}

// Action returns the final action for addr.
func (p *Plan) Action(addr string) (ActionKind, bool) {
	a, ok := p.Actions[addr]
	return a, ok
}

// HasChanges reports whether any resource performs an operation.
func (p *Plan) HasChanges() bool {
	for _, a := range p.Actions {
		if a != ActionNoOp {
			return true
		}
	}
	return false
}

// Execute runs the plan's action graph. See Graph.Execute.
func (p *Plan) Execute(exec Executor) *ExecutionResult {
	if p == nil || p.Graph == nil {
		return &ExecutionResult{Err: &PlanError{Code: ErrorInvalidGraph, Message: "plan has no action graph"}}
	}
	return p.Graph.Execute(exec)
}

// ExecuteWithFailures runs the plan with deterministic failure injection.
func (p *Plan) ExecuteWithFailures(failures map[string]error) *ExecutionResult {
	if p == nil || p.Graph == nil {
		return &ExecutionResult{Err: &PlanError{Code: ErrorInvalidGraph, Message: "plan has no action graph"}}
	}
	return p.Graph.ExecuteWithFailures(failures)
}

// Simulate is an alias for ExecuteWithFailures.
func (p *Plan) Simulate(failures map[string]error) *ExecutionResult {
	return p.ExecuteWithFailures(failures)
}

func (p *Plan) String() string {
	mode := "apply"
	if p.Destroy {
		mode = "destroy"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s plan: %d resources, %d action nodes, %d waves\n", mode, len(p.Resources), len(p.Graph.Nodes), len(p.Waves))
	for _, r := range p.Resources {
		fmt.Fprintf(&b, "  %-30s %s (%s)\n", r.Address, r.Action, r.Reason)
	}
	for _, w := range p.Waves {
		fmt.Fprintf(&b, "  wave %d: %s\n", w.Index, strings.Join(w.NodeIDs, ", "))
	}
	return b.String()
}

func diffAttributes(desired, remote map[string]any) []string {
	keys := make(map[string]bool)
	for k := range desired {
		keys[k] = true
	}
	for k := range remote {
		keys[k] = true
	}
	var changed []string
	for k := range keys {
		dv, dok := desired[k]
		rv, rok := remote[k]
		if dok != rok || !reflect.DeepEqual(dv, rv) {
			changed = append(changed, k)
		}
	}
	sort.Strings(changed)
	return changed
}

func intersects(values, set []string) bool {
	if len(values) == 0 || len(set) == 0 {
		return false
	}
	have := make(map[string]bool, len(set))
	for _, s := range set {
		have[s] = true
	}
	for _, v := range values {
		if have[v] {
			return true
		}
	}
	return false
}

func containsString(in []string, v string) bool {
	for _, s := range in {
		if s == v {
			return true
		}
	}
	return false
}

func findAddressCycle(ids []string, deps map[string][]string) []string {
	g := NewGraph()
	for _, id := range ids {
		_ = g.AddNode(&Node{ID: id, Address: id})
	}
	for _, id := range ids {
		for _, dep := range deps[id] {
			if g.HasNode(dep) {
				_ = g.AddEdge(id, dep)
			}
		}
	}
	return g.FindMinimalCycle()
}
