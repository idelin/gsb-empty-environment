package seq_test

import (
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"seq"
)

func TestFileStorePersistsAcrossInstances(t *testing.T) {
	dir, err := ioutil.TempDir("", "seq")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "state.json")

	a, _ := seq.NewAllocator(seq.NewFileStore(path), 8)
	for i := 0; i < 3; i++ {
		mustNext(t, a)
	}

	// A new store instance on the same file (a "restart") sees the
	// persisted high-water mark and skips the unused tail.
	b, _ := seq.NewAllocator(seq.NewFileStore(path), 8)
	if id := mustNext(t, b); id != 8 {
		t.Fatalf("got %d, want 8", id)
	}
}

// TestFileStoreCrossProcess runs two real OS processes against one state
// file and verifies their issued IDs are disjoint.
func TestFileStoreCrossProcess(t *testing.T) {
	dir, err := ioutil.TempDir("", "seq")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	statePath := filepath.Join(dir, "state.json")

	const procs = 2
	const perProc = 300
	outs := make([]string, procs)
	cmds := make([]*exec.Cmd, procs)
	for p := 0; p < procs; p++ {
		outs[p] = filepath.Join(dir, fmt.Sprintf("ids.%d", p))
		cmd := exec.Command(os.Args[0], "-test.run=TestHelperProcess")
		cmd.Env = append(os.Environ(),
			"SEQ_HELPER=1",
			"SEQ_STATE="+statePath,
			"SEQ_OUT="+outs[p],
			"SEQ_COUNT="+strconv.Itoa(perProc),
		)
		cmds[p] = cmd
	}
	for _, cmd := range cmds {
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
	}
	for _, cmd := range cmds {
		if err := cmd.Wait(); err != nil {
			t.Fatalf("helper: %v", err)
		}
	}

	seen := make(map[uint64]bool)
	total := 0
	for _, out := range outs {
		data, err := ioutil.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			id, err := strconv.ParseUint(line, 10, 64)
			if err != nil {
				t.Fatal(err)
			}
			if seen[id] {
				t.Fatalf("id %d issued by two processes", id)
			}
			seen[id] = true
			total++
		}
	}
	if total != procs*perProc {
		t.Fatalf("got %d ids, want %d", total, procs*perProc)
	}
}

// TestHelperProcess is re-executed as a child process by
// TestFileStoreCrossProcess.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("SEQ_HELPER") != "1" {
		return
	}
	a, err := seq.NewAllocator(seq.NewFileStore(os.Getenv("SEQ_STATE")), 13)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	n, _ := strconv.Atoi(os.Getenv("SEQ_COUNT"))
	var b strings.Builder
	for i := 0; i < n; i++ {
		id, err := a.Next()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Fprintf(&b, "%d\n", id)
	}
	if err := ioutil.WriteFile(os.Getenv("SEQ_OUT"), []byte(b.String()), 0644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}
