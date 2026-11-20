package prescreen_test

import (
	"crypto/ed25519"
	"fmt"

	"prescreen.local/ddd-10497-v4/prescreen"
)

func Example() {
	rootPub, root, _ := ed25519.GenerateKey(nil)
	subjPub, subj, _ := ed25519.GenerateKey(nil)

	logical := prescreen.ObjectRef{Tenant: "t", Object: "logical", Version: 1}
	physical := prescreen.ObjectRef{Tenant: "t", Object: "physical", Version: 2}

	// A rank-deficient view edge: output bit 0 is erased, aliasing pairs of
	// logical blocks onto the same physical block.
	project := prescreen.IdentityMap()
	project.Rows[0] = 0
	edge := prescreen.ViewEdge{
		From:          logical,
		To:            physical,
		ParentVersion: logical.Version,
		Map:           project,
	}
	manifest, _ := prescreen.SignView("project", edge, root)
	policy := prescreen.Policy{
		Roots:     []ed25519.PublicKey{rootPub},
		Manifests: []prescreen.ViewManifest{manifest},
	}

	// Grant the full physical space and sign the requesting subject.
	grant, _ := prescreen.SignCapability(prescreen.Capability{
		Kind:    prescreen.CapGrant,
		Subject: subjPub,
		Tenant:  "t",
		Object:  physical,
		Action:  prescreen.ActionRead,
		Slices:  []prescreen.Affine{prescreen.FullSpace()},
	}, root)

	request, _ := prescreen.SignRequest(prescreen.ReadRequest{
		Tenant: "t",
		Object: logical,
		Action: prescreen.ActionRead,
		Slices: []prescreen.Affine{prescreen.FullSpace()},
		Chain:  []string{"project"},
	}, subj)

	ticket, err := prescreen.Evaluate(policy, request, []prescreen.Capability{grant})
	if err != nil {
		fmt.Println("admission error:", err)
		return
	}
	fmt.Println("distinct physical blocks:", ticket.Objects[0].DistinctBlocks)

	if err := prescreen.VerifyTicket(policy, request,
		[]prescreen.Capability{grant}, ticket); err != nil {
		fmt.Println("verification error:", err)
		return
	}
	fmt.Println("independent verification: ok")
	// Output:
	// distinct physical blocks: 140737488355328
	// independent verification: ok
}
