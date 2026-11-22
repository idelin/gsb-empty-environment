package redaction_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"prescreen.local/ps-b149c13498/redaction"
)

func Example() {
	payloads := [][]byte{
		[]byte("login:alice"),
		[]byte("transfer:100"),
		[]byte("login:bob"),
		[]byte("logout:alice"),
	}

	prover, err := redaction.NewProver("audit-2026-11", payloads)
	if err != nil {
		panic(err)
	}

	// Disclose entries 1 and 3; keep entries 0 and 2 redacted.
	bundle := prover.Export(func(index int) bool {
		return index == 1 || index == 3
	})

	encoded, err := json.Marshal(bundle)
	if err != nil {
		panic(err)
	}

	var received redaction.Bundle
	if err := json.Unmarshal(encoded, &received); err != nil {
		panic(err)
	}
	if err := redaction.Verify(&received); err != nil {
		panic(err)
	}

	publicCount := 0
	for _, item := range received.Items {
		if !item.Hidden {
			publicCount++
		}
	}
	fmt.Printf("log=%s total=%d disclosed=%d verified=true\n",
		received.LogID, received.Total, publicCount)
	// Output: log=audit-2026-11 total=4 disclosed=2 verified=true
}

func TestAccessorsAndMalformedInputs(t *testing.T) {
	prover, err := redaction.NewProver("log-Z", makePayloads(6))
	if err != nil {
		t.Fatal(err)
	}
	if prover.LogID() != "log-Z" || prover.Total() != 6 {
		t.Fatal("accessor mismatch")
	}

	commitment, err := prover.CommitmentAt(3)
	if err != nil {
		t.Fatal(err)
	}
	want := redaction.Commitment([]byte("log-Z"), 3, makePayloads(6)[3])
	if string(commitment) != string(want) {
		t.Fatal("CommitmentAt disagrees with Commitment")
	}
	if _, err := prover.CommitmentAt(99); !errors.Is(err, redaction.ErrInvalidBundle) {
		t.Fatalf("out-of-range index: got %v", err)
	}

	if err := redaction.Verify(nil); !errors.Is(err, redaction.ErrInvalidBundle) {
		t.Fatalf("nil bundle: got %v", err)
	}

	bundle := cloneBundle(prover.Export(func(i int) bool { return true }))
	bundle.Items[2].MerklePath[0] = bundle.Items[2].MerklePath[0][:31]
	if err := redaction.Verify(bundle); !errors.Is(err, redaction.ErrInvalidSibling) {
		t.Fatalf("short sibling: got %v", err)
	}
}
