package npmverify_test

import (
	"context"
	"fmt"
	"os"

	"example.org/netsec/q0081/npmverify"
)

// ExampleVerify demonstrates the full trust chain a proxy runs after
// resolving a dist-tag. The signature callback is where the proxy injects
// the registry keys it trusts.
func ExampleVerify() {
	// Documents and tarball would normally be fetched from the mirror.
	doc := &npmverify.Packument{
		Name:     "left-pad",
		DistTags: map[string]string{"latest": "1.4.2"},
	}
	tarball := []byte("downloaded from dist.tarball")

	// The callback owns key management; here it refuses everything. A real
	// proxy looks the keyid up in its trusted registry key store.
	verifier := func(_ context.Context, _ string, _ npmverify.Signature) error {
		return fmt.Errorf("no trusted key")
	}

	_, err := npmverify.Verify(context.Background(), doc, "latest", tarball, verifier, nil)
	if err != nil {
		fmt.Fprintln(os.Stdout, "rejected:", err)
	}

	// Output:
	// rejected: npmverify: dist-tag cannot be resolved to a published version: latest -> 1.4.2
}
