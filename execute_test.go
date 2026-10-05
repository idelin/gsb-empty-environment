package rename

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// writeFiles creates files under dir, each file's content being its identity.
func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// readFiles returns rel-path -> content for every file under dir.
func readFiles(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out[rel] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func diskExists(dir string) func(string) bool {
	return func(rel string) bool {
		_, err := os.Lstat(filepath.Join(dir, rel))
		return err == nil
	}
}

func diskRename(dir string) RenameFunc {
	return func(src, dst string) error {
		return os.Rename(filepath.Join(dir, src), filepath.Join(dir, dst))
	}
}

func TestExecuteCycleOnDisk(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"a": "A", "b": "B", "c": "C"})

	renames := []Rename{{"a", "b"}, {"b", "c"}, {"c", "a"}}
	plan, err := Build(renames, diskExists(dir))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	var log bytes.Buffer
	if err := Execute(plan.Ops, diskRename(dir), NewJournal(&log)); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	want := map[string]string{"a": "C", "b": "A", "c": "B"}
	if got := readFiles(t, dir); !reflect.DeepEqual(got, want) {
		t.Fatalf("final state = %v, want %v", got, want)
	}
	// Journal records BEGIN+DONE for every op.
	if got := bytes.Count(log.Bytes(), []byte("BEGIN")); got != len(plan.Ops) {
		t.Fatalf("journal has %d BEGINs, want %d", got, len(plan.Ops))
	}
	if got := bytes.Count(log.Bytes(), []byte("DONE")); got != len(plan.Ops) {
		t.Fatalf("journal has %d DONEs, want %d", got, len(plan.Ops))
	}
}

// crashRename wraps a real rename and simulates a crash at op crashAt.
// If crashAfterRename is true, the rename itself is performed but the
// failure is reported anyway (crash between rename and DONE journal).
func crashRename(real RenameFunc, crashAt int, crashAfterRename bool) (RenameFunc, *int) {
	crash := errors.New("simulated crash")
	i := 0
	return func(src, dst string) error {
		if i == crashAt {
			i++
			if crashAfterRename {
				if err := real(src, dst); err != nil {
					return err
				}
			}
			return crash
		}
		i++
		return real(src, dst)
	}, &i
}

func TestRecoverAfterPartialFailure(t *testing.T) {
	renames := []Rename{{"a", "b"}, {"b", "c"}, {"c", "a"}}
	initial := map[string]string{"a": "A", "b": "B", "c": "C"}
	want := map[string]string{"a": "C", "b": "A", "c": "B"}

	for _, crashAfterRename := range []bool{false, true} {
		for crashAt := 0; crashAt < 4; crashAt++ {
			dir := t.TempDir()
			writeFiles(t, dir, initial)
			plan, err := Build(renames, diskExists(dir))
			if err != nil {
				t.Fatalf("Build: %v", err)
			}

			// First run crashes at op crashAt.
			var log bytes.Buffer
			crashy, _ := crashRename(diskRename(dir), crashAt, crashAfterRename)
			if err := Execute(plan.Ops, crashy, NewJournal(&log)); err == nil {
				t.Fatalf("afterRename=%v crashAt=%d: expected crash", crashAfterRename, crashAt)
			}

			// Recovery figures out what remains from journal + disk.
			remaining, err := Recover(bytes.NewReader(log.Bytes()), plan.Ops, diskExists(dir))
			if err != nil {
				t.Fatalf("afterRename=%v crashAt=%d: Recover: %v", crashAfterRename, crashAt, err)
			}
			if err := Execute(remaining, diskRename(dir), NewJournal(&log)); err != nil {
				t.Fatalf("afterRename=%v crashAt=%d: resume Execute: %v", crashAfterRename, crashAt, err)
			}
			if got := readFiles(t, dir); !reflect.DeepEqual(got, want) {
				t.Fatalf("afterRename=%v crashAt=%d: final = %v, want %v",
					crashAfterRename, crashAt, got, want)
			}
		}
	}
}

func TestRecoverCompletedPlanIsNoOp(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{"a": "A", "b": "B"})
	renames := []Rename{{"a", "b"}, {"b", "a"}}
	plan, err := Build(renames, diskExists(dir))
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	var log bytes.Buffer
	if err := Execute(plan.Ops, diskRename(dir), NewJournal(&log)); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	remaining, err := Recover(bytes.NewReader(log.Bytes()), plan.Ops, diskExists(dir))
	if err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("remaining = %v, want none", remaining)
	}
}

func TestRecoverRejectsMismatchedJournal(t *testing.T) {
	ops := []Op{{Src: "a", Dst: "b"}}
	journal := bytes.NewBufferString("BEGIN 0 a WRONG\n")
	if _, err := Recover(journal, ops, existsFrom()); err == nil {
		t.Fatal("Recover accepted a journal that does not match the plan")
	}
}
