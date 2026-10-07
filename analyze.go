package iflow

import (
	"fmt"
	"sort"
)

// taint is the abstract value tracked per variable: a label plus the
// provenance node that produced it.
type taint struct {
	label Label
	node  *ProvNode
}

// DeclassEvent records one declassification attempt, honored or rejected.
type DeclassEvent struct {
	Target string
	Source string
	Auth   string
	From   string
	To     string
	OK     bool
	Reason string
}

// SinkVerdict is the per-sink analysis result: whether the flow is allowed,
// the merged label that reached the sink, and the shortest propagation chain
// from a labelled source to the sink (empty for untainted values).
type SinkVerdict struct {
	Sink     string
	Receiver string
	Allowed  bool
	Label    string
	Chain    []string
}

// Report is the full analysis output.
type Report struct {
	Sinks      []SinkVerdict
	Declass    []DeclassEvent
	Violations []string
}

type state struct {
	env    map[string]taint
	pc     Label
	pcNode *ProvNode
}

func (s *state) clone() *state {
	env := make(map[string]taint, len(s.env))
	for k, v := range s.env {
		env[k] = v
	}
	return &state{env: env, pc: s.pc, pcNode: s.pcNode}
}

type analyzer struct {
	lat         *Lattice
	prog        *Program
	prov        *ProvGraph
	rep         *Report
	unboundSeen map[string]bool
}

// Analyze runs the forward flow analysis over prog.
//
// Assignment merges the value label with the current control-dependence (pc)
// label. The pc label is monotone: entering a branch joins the condition's
// label into the pc, and leaving the branch does NOT drop it. Declassification
// is only possible at explicit OpDeclass nodes carrying an authorization
// listed in prog.Authorizations, and only if the new label is no more
// restrictive than the old one; every attempt is recorded.
func Analyze(prog *Program, lat *Lattice) *Report {
	a := &analyzer{
		lat:         lat,
		prog:        prog,
		prov:        &ProvGraph{},
		rep:         &Report{},
		unboundSeen: map[string]bool{},
	}
	st := &state{env: map[string]taint{}, pc: BottomLabel(lat)}
	names := make([]string, 0, len(prog.Inputs))
	for n := range prog.Inputs {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		lbl := prog.Inputs[n]
		var node *ProvNode
		if !lbl.IsBottom() {
			node = a.prov.Add(fmt.Sprintf("input %s : %s", n, lbl))
		}
		st.env[n] = taint{label: lbl, node: node}
	}
	a.execBlock(st, prog.Body)
	return a.rep
}

func (a *analyzer) execBlock(st *state, body []Instr) {
	for _, in := range body {
		switch in.Kind {
		case OpAssign:
			a.execAssign(st, in)
		case OpIf:
			a.execIf(st, in)
		case OpDeclass:
			a.execDeclass(st, in)
		case OpSink:
			a.execSink(st, in)
		}
	}
}

func appendNode(nodes []*ProvNode, n *ProvNode) []*ProvNode {
	if n == nil {
		return nodes
	}
	for _, m := range nodes {
		if m == n {
			return nodes
		}
	}
	return append(nodes, n)
}

func (a *analyzer) evalExpr(st *state, e *Expr) (Label, []*ProvNode) {
	switch e.Kind {
	case ConstExpr:
		return BottomLabel(a.lat), nil
	case VarExpr:
		t, ok := st.env[e.Var]
		if !ok {
			if !a.unboundSeen[e.Var] {
				a.unboundSeen[e.Var] = true
				a.rep.Violations = append(a.rep.Violations,
					fmt.Sprintf("read of unassigned variable %q (treated as public constant)", e.Var))
			}
			return BottomLabel(a.lat), nil
		}
		if t.node != nil {
			return t.label, []*ProvNode{t.node}
		}
		return t.label, nil
	default:
		ll, ln := a.evalExpr(st, e.L)
		rl, rn := a.evalExpr(st, e.R)
		nodes := ln
		for _, n := range rn {
			nodes = appendNode(nodes, n)
		}
		return ll.Join(rl), nodes
	}
}

func (a *analyzer) execAssign(st *state, in Instr) {
	lbl, nodes := a.evalExpr(st, in.Expr)
	lbl = lbl.Join(st.pc)
	nodes = appendNode(nodes, st.pcNode)
	var node *ProvNode
	if len(nodes) > 0 {
		node = a.prov.Add(fmt.Sprintf("%s := %s", in.Target, in.Expr), nodes...)
	}
	st.env[in.Target] = taint{label: lbl, node: node}
}

func (a *analyzer) execIf(st *state, in Instr) {
	condLbl, condNodes := a.evalExpr(st, in.Cond)
	branchPC := st.pc.Join(condLbl)
	branchNode := st.pcNode
	var prev []*ProvNode
	for _, n := range condNodes {
		prev = appendNode(prev, n)
	}
	prev = appendNode(prev, st.pcNode)
	if len(prev) > 0 {
		branchNode = a.prov.Add(fmt.Sprintf("control dependency: if %s", in.Cond), prev...)
	}
	thenSt := st.clone()
	thenSt.pc, thenSt.pcNode = branchPC, branchNode
	elseSt := st.clone()
	elseSt.pc, elseSt.pcNode = branchPC, branchNode
	a.execBlock(thenSt, in.Then)
	a.execBlock(elseSt, in.Else)
	// The pc label deliberately survives the end of the branch: it is the
	// join of both branch exits and is never popped.
	*st = *a.mergeStates(thenSt, elseSt)
}

func (a *analyzer) mergeStates(x, y *state) *state {
	out := &state{env: map[string]taint{}, pc: x.pc.Join(y.pc)}
	out.pcNode = a.mergeNodes("merge of control-dependence labels", x.pcNode, y.pcNode)
	keys := map[string]bool{}
	for k := range x.env {
		keys[k] = true
	}
	for k := range y.env {
		keys[k] = true
	}
	for k := range keys {
		tx, okx := x.env[k]
		ty, oky := y.env[k]
		switch {
		case okx && !oky:
			out.env[k] = tx
		case oky && !okx:
			out.env[k] = ty
		default:
			lbl := tx.label.Join(ty.label)
			node := a.mergeNodes(fmt.Sprintf("merge of %s across branches", k), tx.node, ty.node)
			out.env[k] = taint{label: lbl, node: node}
		}
	}
	return out
}

func (a *analyzer) mergeNodes(descr string, x, y *ProvNode) *ProvNode {
	if x == y {
		return x
	}
	if x == nil {
		return y
	}
	if y == nil {
		return x
	}
	return a.prov.Add(descr, x, y)
}

func (a *analyzer) execDeclass(st *state, in Instr) {
	src, ok := st.env[in.Source]
	if !ok {
		a.rep.Violations = append(a.rep.Violations,
			fmt.Sprintf("declassify of unassigned variable %q ignored", in.Source))
		return
	}
	event := DeclassEvent{Target: in.Target, Source: in.Source, Auth: in.Auth, From: src.label.String()}
	reject := func(reason string) {
		event.OK = false
		event.Reason = reason
		a.rep.Declass = append(a.rep.Declass, event)
		a.rep.Violations = append(a.rep.Violations,
			fmt.Sprintf("declassify %s -> %s rejected: %s", in.Source, in.Target, reason))
		st.env[in.Target] = src // label NOT shrunk
	}
	if !a.prog.Authorizations[in.Auth] {
		reject(fmt.Sprintf("authorization %q not granted", in.Auth))
		return
	}
	toLevel, err := a.lat.LevelOf(in.ToLevel)
	if err != nil {
		reject(err.Error())
		return
	}
	newLbl := Label{lat: a.lat, Level: toLevel, Any: src.label.Any}
	if !src.label.Any {
		newLbl.Receivers = copyReceivers(src.label.Receivers)
		for _, r := range in.AddReceivers {
			newLbl.Receivers[r] = true
		}
	}
	if !newLbl.Leq(src.label) {
		reject(fmt.Sprintf("target label %s is not a shrink of %s", newLbl, src.label))
		return
	}
	event.OK = true
	event.To = newLbl.String()
	a.rep.Declass = append(a.rep.Declass, event)
	var prev []*ProvNode
	prev = appendNode(prev, src.node)
	node := a.prov.Add(fmt.Sprintf("declassify %s -> %s [%s => %s, auth=%s]",
		in.Source, in.Target, src.label, newLbl, in.Auth), prev...)
	st.env[in.Target] = taint{label: newLbl, node: node}
}

func (a *analyzer) execSink(st *state, in Instr) {
	lbl, nodes := a.evalExpr(st, in.Expr)
	lbl = lbl.Join(st.pc)
	nodes = appendNode(nodes, st.pcNode)
	verdict := SinkVerdict{
		Sink:     in.SinkName,
		Receiver: in.Receiver,
		Allowed:  lbl.Allows(in.Receiver),
		Label:    lbl.String(),
	}
	if len(nodes) > 0 {
		node := a.prov.Add(fmt.Sprintf("sink %s -> receiver %q", in.SinkName, in.Receiver), nodes...)
		for _, n := range a.prov.ShortestChain(node) {
			verdict.Chain = append(verdict.Chain, n.Descr)
		}
	}
	a.rep.Sinks = append(a.rep.Sinks, verdict)
}
