package flowsec

import "fmt"

type nodeKey string

type provNode struct {
	Var     string
	Version int
	Instr   int
	Kind    string // input, const, assign, merge, declass, sink
	label   Label  // set for root nodes (input, const)
}

type provEdge struct {
	From nodeKey
	To   nodeKey
	Role string // data, pc, merge, declass
}

type binding struct {
	version int
	label   Label
}

type pcEntry struct {
	label Label
	node  nodeKey
	ifID  int
}

// ChainStep is one hop of a propagation chain.
type ChainStep struct {
	Node    string // e.g. "x#2"
	Instr   int
	Kind    string
	ViaRole string // edge role that led into this step ("" for the first)
}

// SinkResult is the verdict for one sink.
type SinkResult struct {
	SinkID   int
	Name     string
	Receiver string
	Allowed  bool
	Reasons  []string
	Label    Label
	Chain    []ChainStep // shortest propagation chain into this sink
}

// Report is the outcome of Analyze.
type Report struct {
	Sinks      []SinkResult
	Violations []string
}

type analyzer struct {
	lat        *Lattice
	prog       *Program
	env        map[string]*binding
	vers       map[string]int
	nodes      map[nodeKey]provNode
	edges      []provEdge
	pc         []pcEntry
	sinks      []SinkResult
	violations []string
}

// Analyze runs the forward flow analysis over prog.
func Analyze(lat *Lattice, prog *Program) *Report {
	a := &analyzer{
		lat:   lat,
		prog:  prog,
		env:   map[string]*binding{},
		vers:  map[string]int{},
		nodes: map[nodeKey]provNode{},
	}
	a.exec(prog.Instrs)
	return &Report{Sinks: a.sinks, Violations: a.violations}
}

func (a *analyzer) newNode(name string, instr int, kind string, label Label) nodeKey {
	a.vers[name]++
	k := nodeKey(fmt.Sprintf("%s#%d", name, a.vers[name]))
	a.nodes[k] = provNode{Var: name, Version: a.vers[name], Instr: instr, Kind: kind, label: label}
	return k
}

func nodeOf(name string, version int) nodeKey {
	return nodeKey(fmt.Sprintf("%s#%d", name, version))
}

func (a *analyzer) varNode(name string) nodeKey {
	return nodeOf(name, a.vers[name])
}

func (a *analyzer) lookup(instr int, name string) *binding {
	b, ok := a.env[name]
	if !ok {
		a.violations = append(a.violations, fmt.Sprintf("instr %d: use of undefined variable %q", instr, name))
		return &binding{label: BottomLabel()}
	}
	return b
}

// pcLabel joins every control-dependency label on the pc stack.
func (a *analyzer) pcLabel() (Label, []nodeKey) {
	lab := BottomLabel()
	seen := map[nodeKey]bool{}
	var nodes []nodeKey
	for _, e := range a.pc {
		lab = JoinLabels(a.lat, lab, e.label)
		if !seen[e.node] {
			seen[e.node] = true
			nodes = append(nodes, e.node)
		}
	}
	return lab, nodes
}

func (a *analyzer) exec(list []Instr) {
	for _, in := range list {
		switch t := in.(type) {
		case *Input:
			lab := Label{Level: t.Level, Receivers: setOf(t.Receivers)}
			a.newNode(t.Name, t.ID, "input", lab)
			a.env[t.Name] = &binding{version: a.vers[t.Name], label: lab}
		case *Const:
			a.newNode(t.Name, t.ID, "const", BottomLabel())
			a.env[t.Name] = &binding{version: a.vers[t.Name], label: BottomLabel()}
		case *Assign:
			a.execAssign(t)
		case *If:
			a.execIf(t)
		case *Declassify:
			a.execDeclassify(t)
		case *Sink:
			a.execSink(t)
		}
	}
}

func (a *analyzer) execAssign(t *Assign) {
	lab := BottomLabel()
	var preds []nodeKey
	for _, s := range t.Srcs {
		b := a.lookup(t.ID, s)
		lab = JoinLabels(a.lat, lab, b.label)
		preds = append(preds, nodeOf(s, b.version))
	}
	pcLab, pcNodes := a.pcLabel()
	lab = JoinLabels(a.lat, lab, pcLab)
	k := a.newNode(t.Dst, t.ID, "assign", Label{})
	for _, p := range preds {
		a.edges = append(a.edges, provEdge{From: p, To: k, Role: "data"})
	}
	for _, p := range pcNodes {
		a.edges = append(a.edges, provEdge{From: p, To: k, Role: "pc"})
	}
	a.env[t.Dst] = &binding{version: a.vers[t.Dst], label: lab}
}

func cloneEnv(env map[string]*binding) map[string]*binding {
	out := make(map[string]*binding, len(env))
	for k, v := range env {
		out[k] = v
	}
	return out
}

func (a *analyzer) execIf(t *If) {
	cond := a.lookup(t.ID, t.Cond)
	a.pc = append(a.pc, pcEntry{label: cond.label, node: nodeOf(t.Cond, cond.version), ifID: t.ID})
	saved := cloneEnv(a.env)
	a.exec(t.Then)
	thenEnv := a.env
	a.env = cloneEnv(saved)
	a.exec(t.Else)
	elseEnv := a.env
	a.pc = a.pc[:len(a.pc)-1]
	a.env = a.mergeEnvs(thenEnv, elseEnv, t.ID)
}

// mergeEnvs joins the two branch environments. Variables touched by
// either branch get a fresh merge node whose label joins both sides;
// the pc influence baked into those labels is kept, not dropped.
func (a *analyzer) mergeEnvs(thenEnv, elseEnv map[string]*binding, ifID int) map[string]*binding {
	out := map[string]*binding{}
	for v, bt := range thenEnv {
		be, ok := elseEnv[v]
		if !ok || be.version == bt.version {
			out[v] = bt
			continue
		}
		lab := JoinLabels(a.lat, bt.label, be.label)
		k := a.newNode(v, ifID, "merge", Label{})
		a.edges = append(a.edges, provEdge{From: nodeOf(v, bt.version), To: k, Role: "merge"})
		a.edges = append(a.edges, provEdge{From: nodeOf(v, be.version), To: k, Role: "merge"})
		out[v] = &binding{version: a.vers[v], label: lab}
	}
	for v, be := range elseEnv {
		if _, ok := thenEnv[v]; !ok {
			out[v] = be
		}
	}
	return out
}

func (a *analyzer) execDeclassify(t *Declassify) {
	src := a.lookup(t.ID, t.Src)
	reject := func(why string) {
		a.violations = append(a.violations, fmt.Sprintf("declassify@%d rejected: %s", t.ID, why))
		// Label unchanged: the value keeps its original protection.
		k := a.newNode(t.Dst, t.ID, "declass", Label{})
		a.edges = append(a.edges, provEdge{From: nodeOf(t.Src, src.version), To: k, Role: "declass-rejected"})
		a.env[t.Dst] = &binding{version: a.vers[t.Dst], label: src.label}
	}
	g, ok := a.prog.Policy.Grants[t.ID]
	switch {
	case !ok:
		reject("no grant in policy")
		return
	case g.Token != t.Token:
		reject("token mismatch")
		return
	case t.ToLevel != g.ToLevel:
		reject(fmt.Sprintf("target level %s not authorized (grant allows %s)",
			a.lat.Name(t.ToLevel), a.lat.Name(g.ToLevel)))
		return
	case !a.lat.Leq(t.ToLevel, src.label.Level):
		reject(fmt.Sprintf("target level %s is not below current %s: only shrinking is allowed",
			a.lat.Name(t.ToLevel), a.lat.Name(src.label.Level)))
		return
	case !subsetOf(t.AddReceivers, g.AddReceivers):
		reject("requested receivers exceed grant")
		return
	}
	newLab := Label{Level: t.ToLevel}
	if src.label.Receivers != nil {
		newLab.Receivers = cloneSet(src.label.Receivers)
		for _, r := range t.AddReceivers {
			newLab.Receivers[r] = true
		}
	}
	newLab.Prov = append(append([]ProvEvent{}, src.label.Prov...), ProvEvent{
		AtInstr:        t.ID,
		FromLevel:      src.label.Level,
		ToLevel:        t.ToLevel,
		AddedReceivers: append([]string(nil), t.AddReceivers...),
		Token:          t.Token,
	})
	k := a.newNode(t.Dst, t.ID, "declass", Label{})
	a.edges = append(a.edges, provEdge{From: nodeOf(t.Src, src.version), To: k, Role: "declass"})
	a.env[t.Dst] = &binding{version: a.vers[t.Dst], label: newLab}
}

func (a *analyzer) execSink(t *Sink) {
	src := a.lookup(t.ID, t.Src)
	pcLab, pcNodes := a.pcLabel()
	lab := JoinLabels(a.lat, src.label, pcLab)
	k := a.newNode(t.Name, t.ID, "sink", Label{})
	a.edges = append(a.edges, provEdge{From: nodeOf(t.Src, src.version), To: k, Role: "data"})
	for _, p := range pcNodes {
		a.edges = append(a.edges, provEdge{From: p, To: k, Role: "pc"})
	}
	res := SinkResult{SinkID: t.ID, Name: t.Name, Receiver: t.Receiver, Label: lab}
	levelOK := a.lat.Leq(lab.Level, t.MaxLevel)
	recvOK := lab.AllowsReceiver(t.Receiver)
	if !levelOK {
		res.Reasons = append(res.Reasons, fmt.Sprintf("level %s exceeds sink max %s",
			a.lat.Name(lab.Level), a.lat.Name(t.MaxLevel)))
	}
	if !recvOK {
		res.Reasons = append(res.Reasons, fmt.Sprintf("receiver %q not in allowed set %v",
			t.Receiver, lab.ReceiverList()))
	}
	res.Allowed = levelOK && recvOK
	// For a denied sink, the chain leads to the nearest root that
	// CAUSES the denial; otherwise to the nearest root at all.
	target := func(nk nodeKey) bool { return len(a.incoming(nk)) == 0 }
	if !res.Allowed {
		target = func(nk nodeKey) bool {
			n, ok := a.nodes[nk]
			if !ok || n.Kind != "input" {
				return false
			}
			return !a.lat.Leq(n.label.Level, t.MaxLevel) || !n.label.AllowsReceiver(t.Receiver)
		}
	}
	res.Chain = a.shortestChain(k, target)
	a.sinks = append(a.sinks, res)
}

func (a *analyzer) incoming(to nodeKey) []provEdge {
	var out []provEdge
	for _, e := range a.edges {
		if e.To == to {
			out = append(out, e)
		}
	}
	return out
}

// shortestChain runs BFS backwards from the sink node to the nearest
// node matching target and returns the path from that node to the sink.
func (a *analyzer) shortestChain(sink nodeKey, target func(nodeKey) bool) []ChainStep {
	in := map[nodeKey][]provEdge{}
	for _, e := range a.edges {
		in[e.To] = append(in[e.To], e)
	}
	prev := map[nodeKey]provEdge{}
	visited := map[nodeKey]bool{sink: true}
	queue := []nodeKey{sink}
	var root nodeKey
	found := false
	for len(queue) > 0 && !found {
		cur := queue[0]
		queue = queue[1:]
		if target(cur) {
			root, found = cur, true
			break
		}
		for _, e := range in[cur] {
			if !visited[e.From] {
				visited[e.From] = true
				prev[e.From] = e
				queue = append(queue, e.From)
			}
		}
	}
	if !found {
		return nil
	}
	path := []nodeKey{root}
	for cur := root; cur != sink; {
		e := prev[cur]
		path = append(path, e.To)
		cur = e.To
	}
	steps := make([]ChainStep, 0, len(path))
	for i, nk := range path {
		n := a.nodes[nk]
		step := ChainStep{Node: string(nk), Instr: n.Instr, Kind: n.Kind}
		if i > 0 {
			step.ViaRole = prev[path[i-1]].Role
		}
		steps = append(steps, step)
	}
	return steps
}
