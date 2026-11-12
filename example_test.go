package netsec

import (
	"context"
	"fmt"
)

// ExamplePolicy shows how a pre-receive hook wires the policy to its object
// database, shared quota and signature-verification callback.
func ExamplePolicy() {
	repo := NewMemoryRepository()
	base := OID("base")
	repo.Add(&MemoryCommit{OID: base, Bytes: 12, Signer: "alice@example.com"})
	tip := OID("tip")
	repo.Add(&MemoryCommit{OID: tip, Parent: []OID{base}, Bytes: 34, Signer: "alice@example.com"})

	quota := NewQuota(1_000_000)
	policy := NewPolicy(repo, quota, MemoryVerifier, "alice@example.com")

	grant, err := policy.Admit(context.Background(), []RefUpdate{
		{Ref: "refs/heads/main", Old: base, New: tip},
	})
	if err != nil {
		fmt.Println("rejected:", err)
		return
	}
	// Objects are accepted: commit the reservation, or Release on failure.
	reserved := grant.Bytes()
	grant.Commit()

	fmt.Printf("admitted %d bytes, %d used\n", reserved, quota.Used())
	// Output: admitted 34 bytes, 34 used
}
