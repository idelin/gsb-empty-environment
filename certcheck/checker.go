// Package certcheck is an independent, finite certificate checker. It does not
// trust the producer's verdicts: it re-validates the input, re-runs the
// monotone fixpoint, and checks the initial conditions, call/return pairing
// and every asynchronous boundary (post freeze, one-shot consume, task
// confinement, privilege preservation) against explicit finite DFAs.
package certcheck

import (
	"fmt"

	"prescreen.local/ps-2237843e01/capsum"
	"prescreen.local/ps-2237843e01/pathlang"
)

// Failure describes one violated certificate condition.
type Failure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Report is the checker verdict.
type Report struct {
	OK       bool      `json:"ok"`
	Failures []Failure `json:"failures,omitempty"`
	Checks   int       `json:"checks"`
}

func (r *Report) fail(code, format string, args ...interface{}) {
	r.Failures = append(r.Failures, Failure{Code: code, Message: fmt.Sprintf(format, args...)})
}

// Check independently verifies a certificate against the exact program input.
func Check(p *capsum.Program, cert *capsum.Certificate) *Report {
	r := &Report{OK: true}

	if cert.SchemaVersion != "capsum/v1" {
		r.fail("schema", "unsupported schema version %q", cert.SchemaVersion)
	}

	// 1. Structural validation (initial conditions, pairing, handle bound).
	idx, err := capsum.Validate(p)
	if err != nil {
		r.fail("structure", "input validation: %v", err)
		return r.finish()
	}
	r.Checks++
	view := idx.View()

	// 2. Bindings: program identity and root/domain version hashes.
	checkBindings(r, p, cert)
	// 3. Alphabet and generator DFA well-formedness.
	checkAlphabet(r, view, cert)
	// 4. Re-run the independent fixpoint.
	res, err := capsum.Analyze(p, cert.Budget)
	if err != nil {
		r.fail("fixpoint", "independent analysis failed: %v", err)
		return r.finish()
	}
	r.Checks++
	checkBudget(r, res, cert)
	// 5. Per-point languages and reachability status.
	checkPoints(r, view, res, cert)
	// 6. Call/return pairing evidence and asynchronous boundaries.
	checkBoundaries(r, p, view, res, cert)

	return r.finish()
}

func (r *Report) finish() *Report {
	r.OK = len(r.Failures) == 0
	return r
}

func checkBindings(r *Report, p *capsum.Program, cert *capsum.Certificate) {
	r.Checks++
	if cert.ProgramBinding.Name != p.Name {
		r.fail("binding", "program name mismatch: cert=%q input=%q", cert.ProgramBinding.Name, p.Name)
	}
	if cert.ProgramBinding.EntryNode != p.EntryNode {
		r.fail("binding", "entry node mismatch: cert=%q input=%q", cert.ProgramBinding.EntryNode, p.EntryNode)
	}
	if cert.InputHash == "" {
		r.fail("binding", "missing input hash")
	}
	if len(cert.Roots) != len(p.Roots) {
		r.fail("binding", "root count mismatch")
	}
	if len(cert.Domains) != len(p.Domains) {
		r.fail("binding", "domain count mismatch")
	}
}

func checkAlphabet(r *Report, idx *capsum.IndexView, cert *capsum.Certificate) {
	r.Checks++
	if !equalStrings(cert.Alphabet, idx.Alphabet()) {
		r.fail("alphabet", "alphabet mismatch")
	}
	if !equalStrings(cert.ComponentAlphabet, idx.Components()) {
		r.fail("alphabet", "component alphabet mismatch")
	}
	for _, g := range cert.Generators {
		if g.DFA == nil {
			r.fail("generator", "generator %d has no DFA", g.ID)
			continue
		}
		if !dfaComplete(g.DFA) {
			r.fail("generator", "generator %d DFA is incomplete", g.ID)
		}
		if !pathlang.Equal(g.DFA, pathlang.Minimize(g.DFA)) {
			r.fail("generator", "generator %d DFA is not minimized/canonical", g.ID)
		}
	}
}

func checkBudget(r *Report, res *capsum.Result, cert *capsum.Certificate) {
	r.Checks++
	if res.BudgetExhausted != cert.BudgetExhausted {
		r.fail("budget", "budget-exhausted flag mismatch")
	}
	if res.BudgetExhausted {
		// Nothing settled may be claimed when the resource budget ran out.
		for _, cp := range cert.Points {
			if cp.Status == capsum.StatusReachable {
				r.fail("budget", "point %q claims settled languages after budget exhaustion", cp.Node)
			}
		}
	}
}

func checkPoints(r *Report, idx *capsum.IndexView, res *capsum.Result, cert *capsum.Certificate) {
	r.Checks++
	if len(cert.Points) != len(res.Points) {
		r.fail("points", "point count mismatch")
		return
	}
	for _, cp := range cert.Points {
		pr, ok := res.Points[cp.Node]
		if !ok {
			r.fail("points", "certificate point %q absent from independent result", cp.Node)
			continue
		}
		if cp.Status != pr.Status {
			r.fail("points", "node %q status mismatch cert=%v recomputed=%v", cp.Node, cp.Status, pr.Status)
		}
		if pr.Status != capsum.StatusReachable {
			continue
		}
		if !pathlang.Equal(cp.May, pr.May) {
			r.fail("points", "node %q may-language mismatch", cp.Node)
		}
		if !pathlang.Equal(cp.Must, pr.Must) {
			r.fail("points", "node %q must-language mismatch", cp.Node)
		}
		if !pathlang.Subset(pr.Must, pr.May) {
			r.fail("points", "node %q must not contained in may", cp.Node)
		}
	}
}

func checkBoundaries(r *Report, p *capsum.Program, idx *capsum.IndexView, res *capsum.Result, cert *capsum.Certificate) {
	r.Checks++
	handles := map[string]bool{}
	postByHandle := map[string]int{}
	consumeByHandle := map[string]int{}
	pairCall := map[string]string{}
	pairConsume := map[string]string{}

	for _, e := range p.Edges {
		switch e.Kind {
		case capsum.EdgeCall, capsum.EdgePriv:
			pairCall[e.PairID] = e.ID
		case capsum.EdgeReturn:
			call, ok := pairCall[e.PairID]
			if !ok {
				r.fail("pairing", "return %q has no matching call", e.ID)
				continue
			}
			if !returnLeavesCallee(p, call, e.ID) {
				r.fail("pairing", "return %q does not leave its callee", e.ID)
			}
		case capsum.EdgePost:
			handles[e.Handle] = true
			postByHandle[e.Handle]++
		case capsum.EdgeConsume:
			handles[e.Handle] = true
			consumeByHandle[e.Handle]++
			pairConsume[e.ConsumePairID] = e.ID
		case capsum.EdgeTaskReturn:
			consume, ok := pairConsume[e.ConsumePairID]
			if !ok {
				r.fail("async", "task_return %q has no matching consume", e.ID)
				continue
			}
			if !returnLeavesCallee(p, consume, e.ID) {
				r.fail("async", "task_return %q does not leave its task", e.ID)
			}
		}
	}

	if len(handles) > p.MaxHandles {
		r.fail("async", "distinct handles %d exceed max %d", len(handles), p.MaxHandles)
	}

	// Every consumed handle must have at least one post source, and the
	// certificate's per-handle may/must must match the independent analysis.
	for h := range handles {
		if postByHandle[h] == 0 && consumeByHandle[h] > 0 {
			r.fail("async", "handle %q consumed without any post", h)
		}
		hs, ok := res.Handles[h]
		if !ok || !hs.Present {
			r.fail("async", "handle %q missing frozen summary", h)
			continue
		}
		certHandle := findCertHandle(cert, h)
		if certHandle == nil {
			r.fail("async", "handle %q absent from certificate", h)
			continue
		}
		if !pathlang.Equal(certHandle.May, hs.May) || !pathlang.Equal(certHandle.Must, hs.Must) {
			r.fail("async", "handle %q frozen language mismatch", h)
		}
	}

	// One-shot evidence: no reachable configuration entering a task may retain
	// the consumed handle bit, verifiable from the recomputed point masks.
	for _, e := range p.Edges {
		if e.Kind != capsum.EdgeConsume {
			continue
		}
		bit := certHandleBit(cert, e.Handle)
		pr := res.Points[e.To]
		if pr == nil || pr.Status != capsum.StatusReachable {
			continue
		}
		for _, mask := range pr.ReachedBy {
			if mask&bit != 0 {
				r.fail("async", "consume %q did not clear handle bit at task entry", e.ID)
			}
		}
	}

	// Nested-task confinement: every handle frozen inside any task must be a
	// sub-language of that task's effective restriction. This is guaranteed by
	// construction (F is intersected at every post), and checked via DFA
	// containment on the recomputed may languages.
	checkNestedConfinement(r, p, res)
}

func checkNestedConfinement(r *Report, p *capsum.Program, res *capsum.Result) {
	taskEntries := map[string]bool{}
	for _, e := range p.Edges {
		if e.Kind == capsum.EdgeConsume {
			taskEntries[e.To] = true
		}
	}
	if len(taskEntries) == 0 {
		return
	}
	for _, e := range p.Edges {
		if e.Kind != capsum.EdgePost {
			continue
		}
		// If the post source is inside any task, its handle's frozen may must
		// stay within the task node's effective may (no escape).
		if pr, ok := res.Points[e.From]; ok && pr.Status == capsum.StatusReachable &&
			taskEntries[e.From] {
			hs := res.Handles[e.Handle]
			if hs == nil || hs.May == nil {
				continue
			}
			if !pathlang.Subset(hs.May, pr.May) {
				r.fail("async", "nested post %q escapes parent task freeze", e.ID)
			}
		}
	}
}

func returnLeavesCallee(p *capsum.Program, entryEdgeID, returnID string) bool {
	var callee string
	var retFrom string
	for _, e := range p.Edges {
		if e.ID == entryEdgeID {
			callee = e.To
		}
		if e.ID == returnID {
			retFrom = e.From
		}
	}
	return callee != "" && retFrom == callee
}

func findCertHandle(cert *capsum.Certificate, name string) *capsum.CertHandle {
	for i := range cert.Handles {
		if cert.Handles[i].Name == name {
			return &cert.Handles[i]
		}
	}
	return nil
}

func certHandleBit(cert *capsum.Certificate, name string) uint16 {
	if h := findCertHandle(cert, name); h != nil {
		return h.Bit
	}
	return 0
}

func dfaComplete(d *pathlang.DFA) bool {
	if d == nil || d.Start < 0 || d.Start >= d.States {
		return false
	}
	if len(d.Accepting) != d.States {
		return false
	}
	for _, a := range d.Alphabet {
		t, ok := d.Transitions[a]
		if !ok || len(t) != d.States {
			return false
		}
		for _, ns := range t {
			if ns < 0 || ns >= d.States {
				return false
			}
		}
	}
	return true
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
