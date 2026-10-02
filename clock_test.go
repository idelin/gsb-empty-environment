package matrixclock

import "testing"

func TestIncrementAndSnapshot(t *testing.T) {
	m := make(MatrixClock)
	m.Increment("A")
	m.Increment("A")
	if got := m["A"]["A"]; got != 2 {
		t.Fatalf("own component = %d, want 2", got)
	}
	snap := m.Snapshot()
	snap["A"]["A"] = 99
	if m["A"]["A"] != 2 {
		t.Fatal("snapshot must be a deep copy")
	}
}

func TestMergeTakesElementWiseMax(t *testing.T) {
	a := NewNode("A", []string{"B"})
	b := NewNode("B", []string{"A"})

	b.LocalEvent("x")
	b.LocalEvent("y")
	msg := b.Send("A", "hello") // B clock = 3
	a.Receive(msg)

	row := a.Matrix()
	if row["A"]["B"] != 3 {
		t.Fatalf("A's vector clock should learn B=3, got %d", row["A"]["B"])
	}
	if row["B"]["B"] != 3 {
		t.Fatalf("A's matrix row for B should record B=3, got %d", row["B"]["B"])
	}
	if row["A"]["A"] != 1 {
		t.Fatalf("receive event must increment own component, got %d", row["A"]["A"])
	}
}

func TestAcknowledgedIsColumnMinimum(t *testing.T) {
	m := MatrixClock{
		"A": {"A": 5, "B": 3},
		"B": {"A": 4, "B": 3},
		"C": {"A": 7}, // C knows nothing about B -> counts as 0
	}
	ack := m.Acknowledged()
	if ack["A"] != 4 {
		t.Fatalf("ack[A] = %d, want 4", ack["A"])
	}
	if ack["B"] != 0 {
		t.Fatalf("ack[B] = %d, want 0 (C has no knowledge of B)", ack["B"])
	}
}

func TestSafeLSNAndTruncate(t *testing.T) {
	n := NewNode("A", []string{"B"})
	n.LocalEvent("e1") // LSN 1, A@1
	n.LocalEvent("e2") // LSN 2, A@2
	n.LocalEvent("e3") // LSN 3, A@3

	// Nobody else has acknowledged anything yet.
	if got := n.SafeLSN(); got != 0 {
		t.Fatalf("SafeLSN with empty peer rows = %d, want 0", got)
	}

	// Simulate peer B acknowledging A@2 (but not A@3).
	n.clock.Merge("A", "B", MatrixClock{"B": {"A": 2, "B": 1}})
	if got := n.SafeLSN(); got != 2 {
		t.Fatalf("SafeLSN = %d, want 2", got)
	}

	truncated := n.Truncate()
	if truncated != 2 {
		t.Fatalf("Truncate returned %d, want 2", truncated)
	}
	log := n.Log()
	if len(log) != 1 || log[0].LSN != 3 {
		t.Fatalf("log after truncate = %v, want only LSN 3", log)
	}
	if n.BaseLSN() != 3 {
		t.Fatalf("BaseLSN = %d, want 3", n.BaseLSN())
	}
}
