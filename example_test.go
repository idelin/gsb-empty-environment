package artifactflow_test

import (
	"fmt"

	artifactflow "prescreen.local/ddd-10563-v3"
)

func ExampleVerify() {
	set := &artifactflow.ReceiptSet{
		Roots: []artifactflow.RootArtifact{{
			ID:     "clean-source",
			Tenant: "tenant-a",
			Path:   "stage/app",
			Digest: "sha256:staged",
			Signer: "builder",
		}},
		Receipts: []artifactflow.Receipt{{
			ID:     "promote",
			Step:   "promote",
			Tenant: "tenant-a",
			Signer: "releaser",
			Inputs: []artifactflow.InputPort{{
				Path:        "release/app",
				Digest:      "sha256:staged",
				Consumption: artifactflow.Consume,
			}},
			Outputs: []artifactflow.OutputPort{{
				Path:   "final/app",
				Digest: "sha256:released",
			}},
			Renames: []artifactflow.RenameRule{{
				From: "stage/{name}",
				To:   "release/{name}",
			}},
		}},
		Finals: []artifactflow.FinalArtifact{{
			Tenant:    "tenant-a",
			Path:      "final/app",
			Digest:    "sha256:released",
			Sensitive: true,
		}},
	}

	result, err := artifactflow.Verify(set)
	if err != nil {
		panic(err)
	}
	fmt.Println(result.Status)
	fmt.Println(result.Certificate.Finals[0].Roots)
	fmt.Println(result.Certificate.Verify(set))
	// Output:
	// verified
	// [clean-source]
	// <nil>
}
