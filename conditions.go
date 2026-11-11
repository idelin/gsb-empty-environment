package netsec

import (
	"fmt"
	"strings"
	"time"
)

// Evidence kinds emitted while evaluating a verified claim tree.
const (
	KindTree          = "tree"
	KindEntryAudience = "entry_audience"
	KindAudience      = "audience"
	KindTime          = "time"
	KindProxy         = "proxy"
	KindProxyForward  = "proxy_forward"
	KindAll           = "all"
	KindAny           = "any"
	KindUnknown       = "unknown"
)

// unlimitedProxy marks a proxy budget without a numeric cap.
const unlimitedProxy = -1

// Evidence records how a single condition (or forwarding decision) evaluated
// at a particular node of the claim tree.
type Evidence struct {
	// NodeID is the node that owns the condition.
	NodeID string
	// Path is the slash-joined chain of node IDs from the root to NodeID.
	Path string
	// Kind is one of the Kind* constants.
	Kind string
	// Satisfied reports whether the condition held.
	Satisfied bool
	// Detail carries human-readable evaluation context.
	Detail string
}

// Report is the result of evaluating a claim tree.
type Report struct {
	// Accepted is true only when every node and condition was satisfied.
	Accepted bool
	// Evidences is the DFS-ordered trace of every evaluated condition.
	Evidences []Evidence
}

// Condition is a node-level assertion condition. The interface is sealed:
// callers can only construct the concrete condition types provided here, so a
// decoded tree can never smuggle in an unrecognized, silently accepted rule.
type Condition interface {
	conditionType() string
	evaluate(ctx *evalCtx) []Evidence
}

// AudienceRestriction is satisfied when the relying party matches at least one
// of the listed audiences (OR inside one restriction). Distinct restrictions
// on the same node are combined with AND by All / the node itself.
type AudienceRestriction struct {
	Audiences []string
}

func (r *AudienceRestriction) conditionType() string { return KindAudience }

func (r *AudienceRestriction) evaluate(ctx *evalCtx) []Evidence {
	satisfied := false
	for _, candidate := range r.Audiences {
		if candidate == ctx.entryAudience && candidate != "" {
			satisfied = true
			break
		}
	}
	return []Evidence{{
		Kind:      KindAudience,
		Satisfied: satisfied,
		Detail: fmt.Sprintf("entry audience=%q; restriction audiences=[%s]",
			ctx.entryAudience, strings.Join(r.Audiences, ",")),
	}}
}

// ValidityWindow is the half-open interval [NotBefore, NotOnOrAfter). A zero
// time means that bound is unbounded. Clock skew is applied once at the entry
// evaluator and reused unchanged on every node.
type ValidityWindow struct {
	NotBefore    time.Time
	NotOnOrAfter time.Time
}

func (w *ValidityWindow) conditionType() string { return KindTime }

func (w *ValidityWindow) evaluate(ctx *evalCtx) []Evidence {
	satisfied := true
	var details []string

	if !w.NotBefore.IsZero() {
		// Grace is fixed at entry: the claim is accepted once now+skew
		// reaches NotBefore, so equality is accepted (half-open lower bound).
		effectiveNow := ctx.now.Add(ctx.skew)
		if effectiveNow.Before(w.NotBefore) {
			satisfied = false
			details = append(details, fmt.Sprintf("not yet valid: now=%s not_before=%s",
				effectiveNow.UTC().Format(time.RFC3339Nano), w.NotBefore.UTC().Format(time.RFC3339Nano)))
		} else {
			details = append(details, "lower bound satisfied")
		}
	}

	if !w.NotOnOrAfter.IsZero() {
		// The same single skew allowance widens the upper bound; equality
		// still rejects because the interval is half-open.
		effectiveNow := ctx.now.Add(-ctx.skew)
		if !effectiveNow.Before(w.NotOnOrAfter) {
			satisfied = false
			details = append(details, fmt.Sprintf("expired: now=%s not_on_or_after=%s",
				effectiveNow.UTC().Format(time.RFC3339Nano), w.NotOnOrAfter.UTC().Format(time.RFC3339Nano)))
		} else {
			details = append(details, "upper bound satisfied")
		}
	}

	return []Evidence{{
		Kind:      KindTime,
		Satisfied: satisfied,
		Detail:    strings.Join(details, "; "),
	}}
}

// ProxyRestriction caps how many further forwarding hops are allowed below the
// owning node. A nil Count means no numeric cap. Each forwarding step to a
// child consumes one unit; Count=0 therefore forbids any child node.
type ProxyRestriction struct {
	Count *int
}

func (p *ProxyRestriction) conditionType() string { return KindProxy }

func (p *ProxyRestriction) evaluate(_ *evalCtx) []Evidence {
	if p.Count == nil {
		return []Evidence{{Kind: KindProxy, Satisfied: true, Detail: "proxy count unlimited"}}
	}
	if *p.Count < 0 {
		return []Evidence{{Kind: KindProxy, Satisfied: false, Detail: "negative proxy count"}}
	}
	return []Evidence{{
		Kind:      KindProxy,
		Satisfied: true,
		Detail:    fmt.Sprintf("proxy count=%d", *p.Count),
	}}
}

// All is satisfied only when every nested condition is satisfied (AND).
type All struct {
	Conditions []Condition
}

func (g *All) conditionType() string { return KindAll }

func (g *All) evaluate(ctx *evalCtx) []Evidence {
	memberEvidence, satisfied := evaluateMembers(g.Conditions, ctx)
	header := Evidence{
		Kind:      KindAll,
		Satisfied: satisfied,
		Detail:    fmt.Sprintf("%d nested condition(s)", len(g.Conditions)),
	}
	return append([]Evidence{header}, memberEvidence...)
}

// Any is satisfied when at least one nested condition is satisfied (OR). An
// empty group is false, and an unknown member still forces denial.
type Any struct {
	Conditions []Condition
}

func (g *Any) conditionType() string { return KindAny }

func (g *Any) evaluate(ctx *evalCtx) []Evidence {
	memberEvidence, _ := evaluateMembers(g.Conditions, ctx)
	satisfied := false
	for _, ev := range memberEvidence {
		if ev.Satisfied {
			satisfied = true
			break
		}
	}
	if len(g.Conditions) == 0 {
		satisfied = false
	}
	header := Evidence{
		Kind:      KindAny,
		Satisfied: satisfied,
		Detail:    fmt.Sprintf("%d nested condition(s)", len(g.Conditions)),
	}
	return append([]Evidence{header}, memberEvidence...)
}

// unknownCondition is the representation of any condition type the decoder did
// not recognize. It always denies, including inside an Any group.
type unknownCondition struct {
	name string
}

// UnknownCondition builds a condition that always evaluates to denial.
func UnknownCondition(name string) Condition {
	return &unknownCondition{name: name}
}

func (u *unknownCondition) conditionType() string { return KindUnknown }

func (u *unknownCondition) evaluate(_ *evalCtx) []Evidence {
	return []Evidence{{
		Kind:      KindUnknown,
		Satisfied: false,
		Detail:    fmt.Sprintf("unknown condition %q is not supported", u.name),
	}}
}

func evaluateMembers(members []Condition, ctx *evalCtx) ([]Evidence, bool) {
	satisfied := len(members) > 0
	var out []Evidence
	for index, member := range members {
		if member == nil {
			satisfied = false
			out = append(out, Evidence{
				Kind:      KindUnknown,
				Satisfied: false,
				Detail:    fmt.Sprintf("nil condition at index %d", index),
			})
			continue
		}
		nested := member.evaluate(ctx)
		for _, ev := range nested {
			if !ev.Satisfied {
				satisfied = false
			}
			out = append(out, ev)
		}
	}
	return out, satisfied
}

// Node is one assertion in the verified claim tree. Its conditions all apply
// at this node; each child represents one proxy forwarding step.
type Node struct {
	ID         string
	Conditions []Condition
	Children   []*Node
}

// Evaluator evaluates claim trees. Skew is captured once here, at the entry of
// the authentication host, and never re-applied per hop.
type Evaluator struct {
	now  time.Time
	skew time.Duration
}

// NewEvaluator builds an entry evaluator. skew is the single clock-skew
// allowance used for every time window in the tree.
func NewEvaluator(now time.Time, skew time.Duration) *Evaluator {
	if skew < 0 {
		skew = 0
	}
	return &Evaluator{now: now.UTC(), skew: skew}
}

// Evaluate walks the verified tree rooted at root for the calling relying party
// entryAudience and returns the full evidence trace.
func (e *Evaluator) Evaluate(root *Node, entryAudience string) *Report {
	report := &Report{Accepted: root != nil}
	if root == nil {
		report.append(Evidence{
			Kind:      KindTree,
			Satisfied: false,
			Detail:    "claim tree root is nil",
		})
		return report
	}

	ctx := &evalCtx{
		entryAudience: entryAudience,
		now:           e.now,
		skew:          e.skew,
		proxyLeft:     unlimitedProxy,
		visiting:      map[*Node]bool{},
		report:        report,
	}

	report.Accepted = e.checkEntryAudience(root, entryAudience, ctx)
	e.evalNode(root, ctx)
	return report
}

// Evaluate is a convenience wrapper around NewEvaluator for one-shot callers.
func Evaluate(root *Node, entryAudience string, now time.Time, skew time.Duration) *Report {
	return NewEvaluator(now, skew).Evaluate(root, entryAudience)
}

func (e *Evaluator) checkEntryAudience(root *Node, entryAudience string, ctx *evalCtx) bool {
	satisfied := entryAudience != ""
	ev := Evidence{
		NodeID:    nodeLabel(root),
		Path:      nodeLabel(root),
		Kind:      KindEntryAudience,
		Satisfied: satisfied,
		Detail:    fmt.Sprintf("entry audience=%q", entryAudience),
	}
	ctx.report.append(ev)
	return satisfied
}

type evalCtx struct {
	entryAudience string
	now           time.Time
	skew          time.Duration
	proxyLeft     int
	path          []string
	visiting      map[*Node]bool
	report        *Report
}

func (e *Evaluator) evalNode(node *Node, parent *evalCtx) {
	path := append(append([]string(nil), parent.path...), nodeLabel(node))

	if parent.visiting[node] {
		parent.report.append(Evidence{
			NodeID:    nodeLabel(node),
			Path:      strings.Join(path, "/"),
			Kind:      KindTree,
			Satisfied: false,
			Detail:    "cycle detected in claim tree",
		})
		reportDenied(parent.report)
		return
	}

	ctx := *parent
	ctx.path = path
	ctx.visiting = cloneVisiting(parent.visiting)
	ctx.visiting[node] = true

	budget := parent.proxyLeft
	for _, condition := range node.Conditions {
		if condition == nil {
			ctx.report.append(stamp(node, path, Evidence{
				Kind:      KindUnknown,
				Satisfied: false,
				Detail:    "nil condition on node",
			}))
			reportDenied(ctx.report)
			continue
		}

		for _, ev := range condition.evaluate(&ctx) {
			ctx.report.append(stamp(node, path, ev))
			if !ev.Satisfied {
				reportDenied(ctx.report)
			}
		}

		if proxy, ok := condition.(*ProxyRestriction); ok {
			budget = tightenBudget(budget, proxy.Count)
			if proxy.Count != nil && *proxy.Count < 0 {
				reportDenied(ctx.report)
			}
		}
	}
	ctx.proxyLeft = budget

	for _, child := range node.Children {
		if child == nil {
			continue
		}
		if budget == 0 {
			ctx.report.append(Evidence{
				NodeID:    nodeLabel(node),
				Path:      strings.Join(path, "/"),
				Kind:      KindProxyForward,
				Satisfied: false,
				Detail:    fmt.Sprintf("proxy budget exhausted; cannot forward to %q", nodeLabel(child)),
			})
			reportDenied(ctx.report)
			continue
		}

		childCtx := ctx
		childCtx.proxyLeft = consumeOne(budget)
		ctx.report.append(Evidence{
			NodeID:    nodeLabel(node),
			Path:      strings.Join(path, "/"),
			Kind:      KindProxyForward,
			Satisfied: true,
			Detail:    fmt.Sprintf("forward to %q (remaining=%d)", nodeLabel(child), childCtx.proxyLeft),
		})
		e.evalNode(child, &childCtx)
	}
}

func (r *Report) append(ev Evidence) {
	r.Evidences = append(r.Evidences, ev)
}

func reportDenied(report *Report) {
	report.Accepted = false
}

func stamp(node *Node, path []string, ev Evidence) Evidence {
	ev.NodeID = nodeLabel(node)
	ev.Path = strings.Join(path, "/")
	return ev
}

func nodeLabel(node *Node) string {
	if node == nil || node.ID == "" {
		return "<unnamed>"
	}
	return node.ID
}

func cloneVisiting(in map[*Node]bool) map[*Node]bool {
	out := make(map[*Node]bool, len(in)+1)
	for node := range in {
		out[node] = true
	}
	return out
}

// tightenBudget combines an inherited remaining budget with a node's own proxy
// count: the strictest applicable cap always wins.
func tightenBudget(inherited int, count *int) int {
	if count == nil {
		return inherited
	}
	if *count < 0 {
		return 0
	}
	if inherited == unlimitedProxy || *count < inherited {
		return *count
	}
	return inherited
}

func consumeOne(budget int) int {
	if budget == unlimitedProxy {
		return unlimitedProxy
	}
	return budget - 1
}
