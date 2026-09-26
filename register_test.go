package majorityregister

import (
	"bytes"
	"fmt"
	"math/rand"
	"testing"
)

func runUntilDone(t *testing.T, sim *Simulation, ticks uint64) {
	t.Helper()
	if !sim.Run(ticks) {
		t.Fatalf("operations did not finish within %d ticks", ticks)
	}
}

func assertReplica(t *testing.T, sim *Simulation, node int, want Tag, value []byte) {
	t.Helper()
	replica, err := sim.Replica(node)
	if err != nil {
		t.Fatal(err)
	}
	got := replica.State()
	if got.Tag != want || !bytes.Equal(got.Value, value) {
		t.Fatalf("node %d = (%s, %q), want (%s, %q)", node, got.Tag, got.Value, want, value)
	}
}

func writeValue(t *testing.T, sim *Simulation, label, value string) *Operation {
	t.Helper()
	client := sim.NewClient("client-" + label)
	op, err := client.StartWrite(label, []byte(value))
	if err != nil {
		t.Fatal(err)
	}
	runUntilDone(t, sim, 100)
	if !op.Completed() {
		t.Fatalf("write %s status = %s, want completed", label, op.Status)
	}
	return op
}

func TestQuorumSizesAndFaultTolerance(t *testing.T) {
	sim, err := New(2, Config{Seed: 1, Timeout: 10})
	if err != nil {
		t.Fatal(err)
	}
	if sim.N() != 5 || sim.F() != 2 || sim.Quorum() != 3 {
		t.Fatalf("got N=%d f=%d q=%d, want 5/2/3", sim.N(), sim.F(), sim.Quorum())
	}
}

func TestMajorityWriteAndRead(t *testing.T) {
	sim, _ := New(1, Config{Seed: 2, Timeout: 10})
	write := writeValue(t, sim, "w1", "v1")

	reader := sim.NewClient("reader")
	read, err := reader.StartRead("r1")
	if err != nil {
		t.Fatal(err)
	}
	runUntilDone(t, sim, 100)

	if !read.Completed() || read.Tag() != write.Tag() || string(read.Value()) != "v1" {
		t.Fatalf("read completed=%v tag=%s value=%q", read.Completed(), read.Tag(), read.Value())
	}
}

func TestReadRepairsStaleMinority(t *testing.T) {
	sim, _ := New(2, Config{Seed: 3, Timeout: 20})
	writeValue(t, sim, "v1-label", "v1")

	for _, node := range []int{3, 4} {
		if err := sim.Crash(node); err != nil {
			t.Fatal(err)
		}
	}
	second := writeValue(t, sim, "v2-label", "v2")

	for _, node := range []int{3, 4} {
		if err := sim.Restart(node); err != nil {
			t.Fatal(err)
		}
	}
	for _, node := range []int{1, 2} {
		if err := sim.Crash(node); err != nil {
			t.Fatal(err)
		}
	}

	reader := sim.NewClient("repair-reader")
	read, err := reader.StartRead("repair-read")
	if err != nil {
		t.Fatal(err)
	}
	runUntilDone(t, sim, 100)

	if !read.Completed() || read.Tag() != second.Tag() || string(read.Value()) != "v2" {
		t.Fatalf("read completed=%v tag=%s value=%q", read.Completed(), read.Tag(), read.Value())
	}
	for node := 0; node < sim.N(); node++ {
		assertReplica(t, sim, node, second.Tag(), []byte("v2"))
	}
}

func TestRestartPreservesDurableStateAndRepairs(t *testing.T) {
	sim, _ := New(1, Config{Seed: 4, Timeout: 20})
	writeValue(t, sim, "old", "v1")

	if err := sim.Crash(1); err != nil {
		t.Fatal(err)
	}
	second := writeValue(t, sim, "new", "v2")

	if err := sim.Restart(1); err != nil {
		t.Fatal(err)
	}
	assertReplica(t, sim, 1, Tag{Version: 1, Label: "old"}, []byte("v1"))

	reader := sim.NewClient("restart-reader")
	read, err := reader.StartRead("restart-read")
	if err != nil {
		t.Fatal(err)
	}
	runUntilDone(t, sim, 100)

	if !read.Completed() || read.Tag() != second.Tag() {
		t.Fatalf("read completed=%v tag=%s, want %s", read.Completed(), read.Tag(), second.Tag())
	}
	assertReplica(t, sim, 1, second.Tag(), []byte("v2"))
}

func TestNoMajorityRemainsUnfinished(t *testing.T) {
	sim, _ := New(1, Config{Seed: 5, Timeout: 5})
	committed := writeValue(t, sim, "before-partition", "committed")

	for _, node := range []int{0, 1} {
		if err := sim.Crash(node); err != nil {
			t.Fatal(err)
		}
	}

	reader := sim.NewClient("minority-reader")
	read, err := reader.StartRead("minority-read")
	if err != nil {
		t.Fatal(err)
	}
	if sim.Run(20) {
		t.Fatal("read reported completion with only one live node")
	}
	if read.Status != Running {
		t.Fatalf("read status = %s, want running/unfinished", read.Status)
	}

	for _, node := range []int{0, 1} {
		if err := sim.Restart(node); err != nil {
			t.Fatal(err)
		}
	}
	runUntilDone(t, sim, 100)
	if !read.Completed() || read.Tag() != committed.Tag() || string(read.Value()) != "committed" {
		t.Fatalf("read after recovery = (%s, %q)", read.Tag(), read.Value())
	}

	for _, node := range []int{0, 1} {
		if err := sim.Crash(node); err != nil {
			t.Fatal(err)
		}
	}
	writer := sim.NewClient("minority-writer")
	write, err := writer.StartWrite("minority-write", []byte("must not commit"))
	if err != nil {
		t.Fatal(err)
	}
	if sim.Run(20) {
		t.Fatal("write reported completion without a majority")
	}
	if write.Status != Running {
		t.Fatalf("write status = %s, want running/unfinished", write.Status)
	}

	for _, node := range []int{0, 1} {
		if err := sim.Restart(node); err != nil {
			t.Fatal(err)
		}
	}
	runUntilDone(t, sim, 100)
	if !write.Completed() {
		t.Fatalf("write after recovery status = %s, want completed", write.Status)
	}
	assertReplica(t, sim, 0, write.Tag(), []byte("must not commit"))
}

func TestOldRepliesFirstCannotRegressRead(t *testing.T) {
	sim, _ := New(2, Config{Seed: 6, Timeout: 100})
	writeValue(t, sim, "first", "v1")

	for _, node := range []int{3, 4} {
		if err := sim.Crash(node); err != nil {
			t.Fatal(err)
		}
	}
	second := writeValue(t, sim, "second", "v2")

	for _, node := range []int{3, 4} {
		if err := sim.Restart(node); err != nil {
			t.Fatal(err)
		}
	}
	for _, node := range []int{0, 1} {
		if err := sim.Crash(node); err != nil {
			t.Fatal(err)
		}
	}

	sim.cfg.Delay = func(info MessageInfo, _ *rand.Rand) uint64 {
		if !info.ToReplica {
			if info.From == 3 || info.From == 4 {
				return 2
			}
			return 10
		}
		return 1
	}

	reader := sim.NewClient("ordering-reader")
	read, err := reader.StartRead("ordering-read")
	if err != nil {
		t.Fatal(err)
	}
	runUntilDone(t, sim, 100)

	if !read.Completed() || read.Tag() != second.Tag() || string(read.Value()) != "v2" {
		t.Fatalf("read = completed:%v (%s, %q), want %s/v2", read.Completed(), read.Tag(), read.Value(), second.Tag())
	}
	assertReplica(t, sim, 3, second.Tag(), []byte("v2"))
	assertReplica(t, sim, 4, second.Tag(), []byte("v2"))
}

func TestRandomReorderingDoesNotRegressRead(t *testing.T) {
	for seed := int64(100); seed < 140; seed++ {
		sim, _ := New(1, Config{Seed: seed, MinDelay: 0, MaxDelay: 9, Timeout: 15})
		first := writeValue(t, sim, fmt.Sprintf("first-%d", seed), "old")

		client := sim.NewClient(fmt.Sprintf("writer-%d", seed))
		second, err := client.StartWrite(fmt.Sprintf("second-%d", seed), []byte("new"))
		if err != nil {
			t.Fatal(err)
		}
		runUntilDone(t, sim, 200)

		reader := sim.NewClient(fmt.Sprintf("reader-%d", seed))
		read, err := reader.StartRead(fmt.Sprintf("read-%d", seed))
		if err != nil {
			t.Fatal(err)
		}
		runUntilDone(t, sim, 200)

		if !second.Completed() || second.Tag().Compare(first.Tag()) <= 0 {
			t.Fatalf("seed %d: second write = %s tag %s", seed, second.Status, second.Tag())
		}
		if !read.Completed() || read.Tag() != second.Tag() || string(read.Value()) != "new" {
			t.Fatalf("seed %d: read = %s (%s, %q), want (%s, new)", seed, read.Status, read.Tag(), read.Value(), second.Tag())
		}
	}
}

func TestUniqueLabelBreaksConcurrentWriteTie(t *testing.T) {
	sim, _ := New(2, Config{Seed: 7, Timeout: 50})
	clientA := sim.NewClient("a")
	clientB := sim.NewClient("b")

	sent := make(map[string]int)
	sim.cfg.Delay = func(info MessageInfo, _ *rand.Rand) uint64 {
		if !info.ToReplica {
			return 1
		}
		sent[info.OpID]++
		switch info.OpID {
		case "write-a":
			if sent[info.OpID] <= sim.N() {
				return 2
			}
			return 3
		case "write-b":
			if sent[info.OpID] <= sim.N() {
				return 1
			}
			return 3
		default:
			return 1
		}
	}

	a, err := clientA.StartWrite("write-a", []byte("a"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := clientB.StartWrite("write-b", []byte("b"))
	if err != nil {
		t.Fatal(err)
	}
	runUntilDone(t, sim, 100)

	if !b.Completed() {
		t.Fatalf("higher-label write status = %s, want completed", b.Status)
	}
	if a.Status != Aborted || a.Completed() {
		t.Fatalf("lower-label write status = %s, want aborted/unfinished", a.Status)
	}
	wantTag := Tag{Version: 1, Label: "write-b"}
	if b.Tag() != wantTag {
		t.Fatalf("winner tag = %s, want %s", b.Tag(), wantTag)
	}
	for node := 0; node < sim.N(); node++ {
		assertReplica(t, sim, node, wantTag, []byte("b"))
	}
}

func TestTagOrder(t *testing.T) {
	cases := []struct {
		a, b Tag
		want int
	}{
		{Tag{1, "a"}, Tag{1, "b"}, -1},
		{Tag{1, "b"}, Tag{2, "a"}, -1},
		{Tag{2, "a"}, Tag{2, "a"}, 0},
		{Tag{2, "b"}, Tag{2, "a"}, 1},
	}
	for _, tc := range cases {
		if got := tc.a.Compare(tc.b); got != tc.want {
			t.Fatalf("%s.Compare(%s) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestDuplicateOperationIDRejected(t *testing.T) {
	sim, _ := New(0, Config{Seed: 8, Timeout: 5})
	client := sim.NewClient("client")
	if _, err := client.StartWrite("same-id", []byte("first")); err != nil {
		t.Fatal(err)
	}
	runUntilDone(t, sim, 100)
	if _, err := client.StartRead("same-id"); err != ErrDuplicateLabel {
		t.Fatalf("duplicate id error = %v, want %v", err, ErrDuplicateLabel)
	}
}
