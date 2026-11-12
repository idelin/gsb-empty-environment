package netsec_test

import (
	"fmt"

	netsec "example.org/netsec/q0481"
)

// ExampleSolve shows the verifier host detecting an encryption key being
// misused as an authentication key and recovering the minimum conflict set.
func ExampleSolve() {
	script := netsec.NewScript()
	_ = script.AddNewKey("new-enc", netsec.K("kA"), "alice", netsec.PurposeEncrypt, "s1")
	_ = script.AddBindKey("bind-alias", netsec.K("authSlot"), netsec.K("kA"))
	_ = script.AddSign("sign-wrong", netsec.K("authSlot"))

	rep := netsec.Solve(script, 0)
	fmt.Println("confusable:", rep.Confusable)
	fmt.Println("clash dimension:", rep.Clashes[0].Dimension)
	fmt.Println("clash values:", rep.Clashes[0].Values)
	fmt.Println("minimum conflict ops:", rep.ConflictOps)
	// Output:
	// confusable: true
	// clash dimension: purpose
	// clash values: [encrypt verify]
	// minimum conflict ops: [bind-alias]
}
