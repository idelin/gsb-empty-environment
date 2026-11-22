package redaction_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"testing"

	"prescreen.local/ps-b149c13498/redaction"
)

func makePayloads(n int) [][]byte {
	out := make([][]byte, n)
	for i := range out {
		out[i] = []byte(fmt.Sprintf("entry-%04d-payload-%d", i, i*7+1))
	}
	return out
}

func cloneBundle(b *redaction.Bundle) *redaction.Bundle {
	raw, err := json.Marshal(b)
	if err != nil {
		panic(err)
	}
	var copy redaction.Bundle
	if err := json.Unmarshal(raw, &copy); err != nil {
		panic(err)
	}
	return &copy
}

func flipByte(data []byte) {
	data[0] ^= 0x80
}

func wantOneOf(t *testing.T, err error, targets ...error) {
	t.Helper()
	for _, target := range targets {
		if errors.Is(err, target) {
			return
		}
	}
	t.Fatalf("got %v, want one of %v", err, targets)
}

// TestHonestExportsRandomSubsets verifies every honest export, across many
// log sizes and arbitrary public subsets (including empty and full subsets).
func TestHonestExportsRandomSubsets(t *testing.T) {
	rng := rand.New(rand.NewPCG(20261122, 42))
	sizes := []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 15, 16, 17, 31, 32, 33, 63, 64, 65, 100}

	for _, n := range sizes {
		prover, err := redaction.NewProver(fmt.Sprintf("log-%d", n), makePayloads(n))
		if err != nil {
			t.Fatalf("NewProver n=%d: %v", n, err)
		}

		selectors := []func(int) bool{
			nil, // every entry hidden
			func(int) bool { return true },
		}
		for trial := 0; trial < 25; trial++ {
			mask := make(map[int]bool, n)
			for i := 0; i < n; i++ {
				mask[i] = rng.IntN(2) == 1
			}
			maskCopy := mask
			selectors = append(selectors, func(i int) bool { return maskCopy[i] })
		}

		for si, isPublic := range selectors {
			bundle := prover.Export(isPublic)
			if bundle.Total != n || len(bundle.Items) != n {
				t.Fatalf("n=%d selector=%d: unexpected bundle shape", n, si)
			}
			if err := redaction.Verify(bundle); err != nil {
				t.Fatalf("n=%d selector=%d: honest export rejected: %v", n, si, err)
			}
		}
	}
}

// TestOmissionRejected drops an entry both with and without repairing Total.
func TestOmissionRejected(t *testing.T) {
	prover, _ := redaction.NewProver("log-A", makePayloads(10))

	bundle := cloneBundle(prover.Export(func(i int) bool { return true }))
	bundle.Items = append(bundle.Items[:4], bundle.Items[5:]...)
	if err := redaction.Verify(bundle); !errors.Is(err, redaction.ErrInvalidBundle) {
		t.Fatalf("omission with stale total: want failure, got %v", err)
	}

	bundle = cloneBundle(prover.Export(func(i int) bool { return true }))
	bundle.Items = append(bundle.Items[:4], bundle.Items[5:]...)
	bundle.Total = len(bundle.Items)
	// Strict canonical ordering catches the gap before the root is rebuilt.
	if err := redaction.Verify(bundle); !errors.Is(err, redaction.ErrIndexOutOfOrder) {
		t.Fatalf("omission with repaired total: want index error, got %v", err)
	}
}

// TestReorderRejected covers positional swaps, swapped serial numbers and
// swapped payloads between public positions.
func TestReorderRejected(t *testing.T) {
	prover, _ := redaction.NewProver("log-A", makePayloads(10))

	swapped := cloneBundle(prover.Export(func(i int) bool { return true }))
	swapped.Items[2], swapped.Items[6] = swapped.Items[6], swapped.Items[2]
	if err := redaction.Verify(swapped); !errors.Is(err, redaction.ErrIndexOutOfOrder) {
		t.Fatalf("positional swap: want index error, got %v", err)
	}

	indexSwap := cloneBundle(prover.Export(func(i int) bool { return true }))
	indexSwap.Items[2].Index, indexSwap.Items[6].Index = 6, 2
	if err := redaction.Verify(indexSwap); !errors.Is(err, redaction.ErrIndexOutOfOrder) {
		t.Fatalf("serial-number swap: want index error, got %v", err)
	}

	payloadSwap := cloneBundle(prover.Export(func(i int) bool { return true }))
	payloadSwap.Items[2].Payload, payloadSwap.Items[6].Payload =
		payloadSwap.Items[6].Payload, payloadSwap.Items[2].Payload
	if err := redaction.Verify(payloadSwap); !errors.Is(err, redaction.ErrCommitmentMismatch) {
		t.Fatalf("payload swap: want commitment error, got %v", err)
	}

	// Hidden entries are pinned to their positions too: swapping hidden
	// commitments changes the rebuilt leaf layer and fails at the root.
	hiddenSwap := cloneBundle(prover.Export(nil))
	hiddenSwap.Items[2].Commitment, hiddenSwap.Items[6].Commitment =
		hiddenSwap.Items[6].Commitment, hiddenSwap.Items[2].Commitment
	if err := redaction.Verify(hiddenSwap); !errors.Is(err, redaction.ErrRootMismatch) {
		t.Fatalf("hidden commitment swap: want root mismatch, got %v", err)
	}
}

// TestDuplicateSerialRejected inserts a duplicated serial in place of another.
func TestDuplicateSerialRejected(t *testing.T) {
	prover, _ := redaction.NewProver("log-A", makePayloads(10))

	bundle := cloneBundle(prover.Export(func(i int) bool { return true }))
	bundle.Items[7] = bundle.Items[2]
	if err := redaction.Verify(bundle); !errors.Is(err, redaction.ErrIndexOutOfOrder) {
		t.Fatalf("duplicated serial: want index error, got %v", err)
	}

	bundle = cloneBundle(prover.Export(nil))
	bundle.Items[7] = bundle.Items[2]
	if err := redaction.Verify(bundle); !errors.Is(err, redaction.ErrIndexOutOfOrder) {
		t.Fatalf("duplicated hidden serial: want index error, got %v", err)
	}
}

// TestCrossLogSpliceRejected mixes material from two logs, including logs
// with identical payloads but different identifiers.
func TestCrossLogSpliceRejected(t *testing.T) {
	payloads := makePayloads(10)
	proverA, _ := redaction.NewProver("log-A", payloads)
	proverB, _ := redaction.NewProver("log-B", payloads)

	// A bundle framed by log A whose root is taken from log B.
	foreignRoot := cloneBundle(proverA.Export(func(i int) bool { return i%2 == 0 }))
	foreignRoot.Root = proverB.Root()
	if err := redaction.Verify(foreignRoot); !errors.Is(err, redaction.ErrRootMismatch) {
		t.Fatalf("foreign root: want root mismatch, got %v", err)
	}

	// Hidden commitments taken from log B spliced into log A.
	spliced := cloneBundle(proverA.Export(nil))
	bBundle := proverB.Export(nil)
	spliced.Items[3].Commitment = bBundle.Items[3].Commitment
	spliced.Items[8].Commitment = bBundle.Items[8].Commitment
	if err := redaction.Verify(spliced); !errors.Is(err, redaction.ErrRootMismatch) {
		t.Fatalf("spliced commitments: want root mismatch, got %v", err)
	}

	// Relabeling an otherwise valid bundle to another log id breaks the root.
	relabeled := cloneBundle(proverA.Export(func(i int) bool { return true }))
	relabeled.LogID = "log-B"
	// With public entries the commitment check fires first; either sentinel
	// proves the log-id binding rejected the forgery.
	wantOneOf(t, redaction.Verify(relabeled),
		redaction.ErrCommitmentMismatch, redaction.ErrRootMismatch)

	// With only hidden entries the recomputed root must reject the new log id.
	relabeledHidden := cloneBundle(proverA.Export(nil))
	relabeledHidden.LogID = "log-B"
	if err := redaction.Verify(relabeledHidden); !errors.Is(err, redaction.ErrRootMismatch) {
		t.Fatalf("relabeled all-hidden log id: want root mismatch, got %v", err)
	}

	// A foreign public item moved to another position fails the strict
	// canonical index-order check first.
	publicSplice := cloneBundle(proverA.Export(func(i int) bool { return true }))
	publicSpliceB := proverB.Export(func(i int) bool { return true })
	publicSplice.Items[5] = publicSpliceB.Items[3]
	if err := redaction.Verify(publicSplice); !errors.Is(err, redaction.ErrIndexOutOfOrder) {
		t.Fatalf("foreign public item moved: want index error, got %v", err)
	}

	// At the same position the foreign item fails because the commitment
	// binds the log id.
	publicSplice2 := cloneBundle(proverA.Export(func(i int) bool { return true }))
	foreign := publicSpliceB.Items[5]
	foreign.Index = 5
	publicSplice2.Items[5] = foreign
	if err := redaction.Verify(publicSplice2); !errors.Is(err, redaction.ErrCommitmentMismatch) {
		t.Fatalf("foreign payload at same index: want commitment error, got %v", err)
	}
}

// TestTamperingRejected covers the remaining forgery surfaces.
func TestTamperingRejected(t *testing.T) {
	prover, _ := redaction.NewProver("log-A", makePayloads(12))

	t.Run("public payload byte", func(t *testing.T) {
		bundle := cloneBundle(prover.Export(func(i int) bool { return true }))
		bundle.Items[4].Payload[0] ^= 0x01
		if err := redaction.Verify(bundle); !errors.Is(err, redaction.ErrCommitmentMismatch) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("hidden commitment byte", func(t *testing.T) {
		bundle := cloneBundle(prover.Export(nil))
		flipByte(bundle.Items[4].Commitment)
		if err := redaction.Verify(bundle); !errors.Is(err, redaction.ErrRootMismatch) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("public commitment byte", func(t *testing.T) {
		bundle := cloneBundle(prover.Export(func(i int) bool { return true }))
		flipByte(bundle.Items[4].Commitment)
		if err := redaction.Verify(bundle); !errors.Is(err, redaction.ErrCommitmentMismatch) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("root byte", func(t *testing.T) {
		bundle := cloneBundle(prover.Export(func(i int) bool { return i%3 == 0 }))
		flipByte(bundle.Root)
		if err := redaction.Verify(bundle); !errors.Is(err, redaction.ErrRootMismatch) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("total count", func(t *testing.T) {
		bundle := cloneBundle(prover.Export(nil))
		bundle.Total = 99
		if err := redaction.Verify(bundle); !errors.Is(err, redaction.ErrInvalidBundle) {
			t.Fatalf("stale total: got %v", err)
		}

		bundle = cloneBundle(prover.Export(nil))
		bundle.Total = 11
		bundle.Items = bundle.Items[:11]
		if err := redaction.Verify(bundle); !errors.Is(err, redaction.ErrRootMismatch) {
			t.Fatalf("trimmed with patched total: got %v", err)
		}
	})

	t.Run("merkle sibling byte", func(t *testing.T) {
		bundle := cloneBundle(prover.Export(func(i int) bool { return true }))
		flipByte(bundle.Items[4].MerklePath[0])
		if err := redaction.Verify(bundle); !errors.Is(err, redaction.ErrRootMismatch) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("merkle path length", func(t *testing.T) {
		bundle := cloneBundle(prover.Export(func(i int) bool { return true }))
		bundle.Items[4].MerklePath = bundle.Items[4].MerklePath[:1]
		if err := redaction.Verify(bundle); !errors.Is(err, redaction.ErrPathLength) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("hidden item leaks payload", func(t *testing.T) {
		bundle := cloneBundle(prover.Export(nil))
		bundle.Items[4].Payload = []byte("leaked")
		if err := redaction.Verify(bundle); !errors.Is(err, redaction.ErrHiddenItemLeak) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("hidden item leaks path", func(t *testing.T) {
		bundle := cloneBundle(prover.Export(nil))
		bundle.Items[4].MerklePath = [][]byte{make([]byte, 32)}
		if err := redaction.Verify(bundle); !errors.Is(err, redaction.ErrHiddenItemLeak) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("public item missing payload", func(t *testing.T) {
		bundle := cloneBundle(prover.Export(func(i int) bool { return true }))
		bundle.Items[4].Payload = nil
		if err := redaction.Verify(bundle); !errors.Is(err, redaction.ErrCommitmentMismatch) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("malformed commitment and root sizes", func(t *testing.T) {
		bundle := cloneBundle(prover.Export(nil))
		bundle.Items[0].Commitment = bundle.Items[0].Commitment[:31]
		if err := redaction.Verify(bundle); !errors.Is(err, redaction.ErrInvalidCommitment) {
			t.Fatalf("short commitment: got %v", err)
		}

		bundle = cloneBundle(prover.Export(nil))
		bundle.Root = bundle.Root[:31]
		if err := redaction.Verify(bundle); !errors.Is(err, redaction.ErrRootSize) {
			t.Fatalf("short root: got %v", err)
		}
	})
}

func TestSingleEntryTree(t *testing.T) {
	prover, err := redaction.NewProver("solo", makePayloads(1))
	if err != nil {
		t.Fatal(err)
	}

	allHidden := prover.Export(nil)
	if len(allHidden.Items[0].MerklePath) != 0 {
		t.Fatal("single-entry tree must have an empty membership path")
	}
	if err := redaction.Verify(allHidden); err != nil {
		t.Fatalf("all-hidden single entry: %v", err)
	}

	allPublic := prover.Export(func(int) bool { return true })
	if err := redaction.Verify(allPublic); err != nil {
		t.Fatalf("all-public single entry: %v", err)
	}

	// Root must bind the total count: claiming two entries cannot validate.
	forked := cloneBundle(allHidden)
	forked.Total = 2
	if err := redaction.Verify(forked); !errors.Is(err, redaction.ErrInvalidBundle) {
		t.Fatalf("forged total: got %v", err)
	}
}

func TestNewProverRejectsEmpty(t *testing.T) {
	if _, err := redaction.NewProver("empty", nil); !errors.Is(err, redaction.ErrEmptyLog) {
		t.Fatalf("empty log: got %v", err)
	}
}

func TestJSONRoundTrip(t *testing.T) {
	prover, _ := redaction.NewProver("log-json", makePayloads(8))
	original := prover.Export(func(i int) bool { return i%2 == 0 })

	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var decoded redaction.Bundle
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if err := redaction.Verify(&decoded); err != nil {
		t.Fatalf("json round trip rejected: %v", err)
	}
	if !decoded.Items[1].Hidden || decoded.Items[0].Hidden {
		t.Fatal("hidden flags did not survive json round trip")
	}
	if string(decoded.Items[0].Payload) != string(makePayloads(8)[0]) {
		t.Fatal("payload did not survive json round trip")
	}
}

// TestDeterminismAndImmutability pins hashing determinism and ensures the
// caller cannot mutate prover state through exported slices.
func TestDeterminismAndImmutability(t *testing.T) {
	payloads := makePayloads(5)
	prover1, _ := redaction.NewProver("same", payloads)
	prover2, _ := redaction.NewProver("same", payloads)

	bundle1 := prover1.Export(func(i int) bool { return i%2 == 1 })
	bundle2 := prover2.Export(func(i int) bool { return true })

	if err := redaction.Verify(bundle1); err != nil {
		t.Fatal(err)
	}
	if err := redaction.Verify(bundle2); err != nil {
		t.Fatal(err)
	}

	// Mutating an exported payload and root must not affect a fresh export.
	bundle1.Items[1].Payload[0] = 0xFF
	bundle1.Root[0] = 0xFF
	fresh := prover1.Export(func(i int) bool { return i%2 == 1 })
	if err := redaction.Verify(fresh); err != nil {
		t.Fatalf("prover state leaked through export: %v", err)
	}

	// Same inputs must yield byte-identical roots.
	if string(prover1.Root()) != string(prover2.Root()) {
		t.Fatal("root construction is not deterministic")
	}
}
