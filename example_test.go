package netsec_test

import (
	"fmt"

	"example.org/netsec/q0390"
)

// ExampleAnalyze models a wrapper whose authentication call can fail and
// whose deferred recovery drops the failure before the success return.
func ExampleAnalyze() {
	g := &netsec.CFG{
		Entry: "auth",
		Blocks: map[string]*netsec.Block{
			"auth": {
				ID: "auth",
				Ops: []netsec.Op{
					netsec.OpAuth{Site: "svc.Authenticate", CredVar: "cred", ErrVar: "err"},
				},
			},
			"ok": {
				ID: "ok",
				Ops: []netsec.Op{
					netsec.OpProtectedCallback{Site: "handler", Name: "IssueToken", Target: "cred"},
					netsec.OpReturn{Site: "ok-return"},
				},
			},
			"recover": {
				ID:  "recover",
				Ops: []netsec.Op{netsec.OpRecover{Site: "defer"}},
			},
			"fallback": {
				ID: "fallback",
				Ops: []netsec.Op{
					netsec.OpErrorAssign{Site: "clear", Var: "err", Source: "nil", Clear: true},
					netsec.OpReturn{Site: "fallback-return"},
				},
			},
		},
		Edges: []netsec.Edge{
			{From: "auth", To: "ok", Kind: netsec.EdgeNormal},
			{From: "auth", To: "recover", Kind: netsec.EdgeException},
			{From: "recover", To: "fallback", Kind: netsec.EdgeNormal},
		},
	}

	report, err := netsec.Analyze(g, netsec.Limits{})
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	fmt.Println("paths:", len(report.Paths))
	for _, finding := range report.Findings {
		fmt.Printf("%s at %s on %d path(s)\n",
			finding.Kind, finding.Site, finding.Count)
	}
	// Output:
	// paths: 2
	// error-masked at clear on 1 path(s)
	// rejection-converted at fallback-return on 1 path(s)
}
