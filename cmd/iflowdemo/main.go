// Command iflowdemo runs the flow analysis and the two-run observational
// check on a few small example programs.
package main

import (
	"fmt"

	"iflow"
)

func lattice() *iflow.Lattice {
	lat, err := iflow.NewLattice(
		[]string{"Public", "Internal", "Secret"},
		[][2]string{{"Public", "Internal"}, {"Internal", "Secret"}},
	)
	if err != nil {
		panic(err)
	}
	return lat
}

func report(name string, prog *iflow.Program, lat *iflow.Lattice) {
	fmt.Printf("=== %s ===\n", name)
	rep := iflow.Analyze(prog, lat)
	for _, s := range rep.Sinks {
		verdict := "DENIED "
		if s.Allowed {
			verdict = "allowed"
		}
		fmt.Printf("  sink %-10s receiver %-8s label %-18s %s\n", s.Sink, s.Receiver, s.Label, verdict)
		for i, step := range s.Chain {
			fmt.Printf("    %d. %s\n", i+1, step)
		}
	}
	for _, d := range rep.Declass {
		status := "rejected: " + d.Reason
		if d.OK {
			status = "honored"
		}
		fmt.Printf("  declass %s -> %s [%s => %s, auth=%s] %s\n", d.Source, d.Target, d.From, d.To, d.Auth, status)
	}
	for _, v := range rep.Violations {
		fmt.Printf("  violation: %s\n", v)
	}
}

func tworun(name string, prog *iflow.Program, in1, in2 map[string]int64) {
	out1, err1 := iflow.Run(prog, in1)
	out2, err2 := iflow.Run(prog, in2)
	if err1 != nil || err2 != nil {
		fmt.Printf("  two-run error: %v %v\n", err1, err2)
		return
	}
	diffs := iflow.CompareRuns(out1, out2)
	fmt.Printf("  two-run %s: run1=%v run2=%v", name, out1, out2)
	if len(diffs) == 0 {
		fmt.Println(" -> sinks indistinguishable for this input pair")
	} else {
		fmt.Printf(" -> OBSERVABLE DIFFERENCE at %v\n", diffs)
	}
}

func main() {
	lat := lattice()
	secret := map[string]iflow.Label{"secret": iflow.NewLabel(lat, "Secret")}

	explicit := &iflow.Program{
		Inputs: secret,
		Body: []iflow.Instr{
			iflow.Assign("x", iflow.V("secret")),
			iflow.Sink("out", "public", iflow.V("x")),
		},
	}
	report("explicit leak", explicit, lat)
	tworun("explicit leak", explicit,
		map[string]int64{"secret": 1}, map[string]int64{"secret": 2})

	implicit := &iflow.Program{
		Inputs: secret,
		Body: []iflow.Instr{
			iflow.Assign("x", iflow.Const(0)),
			iflow.If(iflow.Bin("gt", iflow.V("secret"), iflow.Const(0)),
				[]iflow.Instr{iflow.Assign("x", iflow.Const(1))}, nil),
			iflow.Sink("out", "public", iflow.V("x")),
		},
	}
	report("implicit leak via branch", implicit, lat)
	tworun("implicit leak", implicit,
		map[string]int64{"secret": 0}, map[string]int64{"secret": 5})

	declass := &iflow.Program{
		Inputs:         secret,
		Authorizations: map[string]bool{"ticket-1": true},
		Body: []iflow.Instr{
			iflow.Declass("y", "secret", "Internal", []string{"audit"}, "ticket-1"),
			iflow.Sink("auditLog", "audit", iflow.V("y")),
			iflow.Sink("pubOut", "public", iflow.V("y")),
			iflow.Declass("z", "secret", "Public", []string{"public"}, "forged"),
			iflow.Sink("forgedOut", "public", iflow.V("z")),
		},
	}
	report("authorized + forged declassification", declass, lat)

	overapprox := &iflow.Program{
		Inputs: secret,
		Body: []iflow.Instr{
			iflow.If(iflow.Bin("gt", iflow.V("secret"), iflow.Const(0)),
				[]iflow.Instr{iflow.Assign("tmp", iflow.Const(1))}, nil),
			iflow.Assign("result", iflow.Const(42)),
			iflow.Sink("out", "public", iflow.V("result")),
		},
	}
	report("post-branch constant (conservative false positive)", overapprox, lat)
	tworun("post-branch constant", overapprox,
		map[string]int64{"secret": 0}, map[string]int64{"secret": 9})
}
