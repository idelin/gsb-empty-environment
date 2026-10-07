// Command flowdemo runs the flow analysis on a small leak example and
// cross-validates the verdicts with a second, independent analysis run.
package main

import (
	"fmt"
	"os"
	"strings"

	"flowsec"
)

func main() {
	lat, err := flowsec.ChainLattice("Public", "Confidential", "Secret")
	if err != nil {
		fmt.Fprintln(os.Stderr, "lattice:", err)
		os.Exit(2)
	}
	pub, _ := lat.Index("Public")
	sec, _ := lat.Index("Secret")

	prog := &flowsec.Program{
		Receivers: []string{"alice", "bob"},
		Instrs: []flowsec.Instr{
			// secret is only observable by alice, at level Secret.
			&flowsec.Input{ID: 1, Name: "secret", Level: sec, Receivers: []string{"alice"}},
			&flowsec.Const{ID: 2, Name: "zero"},
			&flowsec.Const{ID: 3, Name: "one"},

			// Direct flow: leak := secret -> bob (expect DENY).
			&flowsec.Assign{ID: 4, Dst: "leak", Srcs: []string{"secret"}},
			&flowsec.Sink{ID: 5, Name: "direct", Src: "leak", Receiver: "bob", MaxLevel: pub},

			// Implicit flow: x depends on secret via the branch condition.
			// The pc dependency must survive the join point.
			&flowsec.Assign{ID: 6, Dst: "x", Srcs: []string{"zero"}},
			&flowsec.If{ID: 7, Cond: "secret",
				Then: []flowsec.Instr{&flowsec.Assign{ID: 8, Dst: "x", Srcs: []string{"one"}}},
				Else: []flowsec.Instr{&flowsec.Assign{ID: 9, Dst: "x", Srcs: []string{"zero"}}},
			},
			&flowsec.Sink{ID: 10, Name: "implicit", Src: "x", Receiver: "bob", MaxLevel: pub},

			// Authorized declassification: y := declassify(x -> Public, +bob).
			&flowsec.Declassify{ID: 11, Dst: "y", Src: "x", ToLevel: pub, AddReceivers: []string{"bob"}, Token: "grant-7f3a"},
			&flowsec.Sink{ID: 12, Name: "declassified", Src: "y", Receiver: "bob", MaxLevel: pub},

			// Same declassification with a WRONG token must be rejected.
			&flowsec.Declassify{ID: 13, Dst: "z", Src: "x", ToLevel: pub, AddReceivers: []string{"bob"}, Token: "forged"},
			&flowsec.Sink{ID: 14, Name: "forged-declass", Src: "z", Receiver: "bob", MaxLevel: pub},
		},
		Policy: flowsec.Policy{Grants: map[int]flowsec.Grant{
			11: {Token: "grant-7f3a", ToLevel: pub, AddReceivers: []string{"bob"}},
		}},
	}

	rep := flowsec.Analyze(lat, prog)
	setVerdicts, setViolations := flowsec.AnalyzeSets(lat, prog)

	fmt.Println("== lattice-based run ==")
	for _, sr := range rep.Sinks {
		verdict := "ALLOW"
		if !sr.Allowed {
			verdict = "DENY "
		}
		fmt.Printf("[%s] sink %-14s receiver=%-6s level=%s receivers=%v\n",
			verdict, sr.Name, sr.Receiver, lat.Name(sr.Label.Level), sr.Label.ReceiverList())
		for _, r := range sr.Reasons {
			fmt.Printf("         reason: %s\n", r)
		}
		var hops []string
		for _, st := range sr.Chain {
			if st.ViaRole == "" {
				hops = append(hops, fmt.Sprintf("%s(%s@%d)", st.Node, st.Kind, st.Instr))
			} else {
				hops = append(hops, fmt.Sprintf("--%s--> %s(%s@%d)", st.ViaRole, st.Node, st.Kind, st.Instr))
			}
		}
		fmt.Printf("         chain:  %s\n", strings.Join(hops, " "))
		for _, p := range sr.Label.Prov {
			fmt.Printf("         provenance: declassified at instr %d: %s -> %s (+%v, token %q)\n",
				p.AtInstr, lat.Name(p.FromLevel), lat.Name(p.ToLevel), p.AddedReceivers, p.Token)
		}
	}
	for _, v := range rep.Violations {
		fmt.Println("violation:", v)
	}

	fmt.Println("\n== independent set-based run (cross-validation) ==")
	for _, sr := range rep.Sinks {
		fmt.Printf("sink %-14s lattice=%-5v set=%-5v\n", sr.Name, sr.Allowed, setVerdicts[sr.SinkID])
	}
	for _, v := range setViolations {
		fmt.Println("set-run violation:", v)
	}

	mismatches := flowsec.CrossValidate(rep, setVerdicts)
	if len(mismatches) > 0 {
		fmt.Println("\nCROSS-VALIDATION FAILED:")
		for _, m := range mismatches {
			fmt.Println(" -", m)
		}
		os.Exit(1)
	}
	fmt.Println("\ncross-validation: both runs agree on all sinks")
}
