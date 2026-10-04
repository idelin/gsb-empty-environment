package delta

import (
	"bytes"
	"errors"
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"
)

const testBlock = 8

var (
	blkA = []byte("abcdefgh")
	blkB = []byte("ABCDEFGH")
	blkC = []byte("12345678")
	blkD = []byte("!@#$%^&*")
)

// countingSource serves blocks of data and counts downloads.
type countingSource struct {
	data    []byte
	block   int
	fetches int
}

func (s *countingSource) FetchBlock(i int) ([]byte, error) {
	s.fetches++
	off := i * s.block
	end := off + s.block
	if end > len(s.data) {
		end = len(s.data)
	}
	return s.data[off:end], nil
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := ioutil.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
}

func planFor(t *testing.T, newData, oldData []byte) *Plan {
	t.Helper()
	m, err := BuildManifest(bytes.NewReader(newData), testBlock)
	if err != nil {
		t.Fatal(err)
	}
	return BuildPlan(m, oldData)
}

// Same content appears at several positions in the new file and at a
// shifted position in the old file: every occurrence must reuse the same
// verified old-file offset.
func TestReuseSameContentMultiplePositions(t *testing.T) {
	newData := concat(blkA, blkA, blkA, blkB)
	oldData := concat([]byte("xy"), blkA, []byte("zw")) // blkA moved to offset 2

	plan := planFor(t, newData, oldData)
	if len(plan.Ops) != 4 {
		t.Fatalf("want 4 ops, got %d", len(plan.Ops))
	}
	for i := 0; i < 3; i++ {
		op := plan.Ops[i]
		if !op.Reuse || op.OldOffset != 2 {
			t.Fatalf("op %d: want reuse from old offset 2, got %+v", i, op)
		}
	}
	if plan.Ops[3].Reuse {
		t.Fatalf("op 3 should be a download, got %+v", plan.Ops[3])
	}

	dir, cleanup := tempDir(t)
	defer cleanup()
	oldPath := filepath.Join(dir, "old.bin")
	newPath := filepath.Join(dir, "new.bin")
	writeFile(t, oldPath, oldData)

	src := &countingSource{data: newData, block: testBlock}
	if err := Apply(plan, oldPath, newPath, &Options{Source: src}); err != nil {
		t.Fatal(err)
	}
	got, err := ioutil.ReadFile(newPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, newData) {
		t.Fatalf("result mismatch:\n got %q\nwant %q", got, newData)
	}
	if src.fetches != 1 {
		t.Fatalf("want exactly 1 download, got %d", src.fetches)
	}
}

// The new file's last block is shorter than the block size and matches
// the old file's tail: it must be reused, and the result must be exact.
func TestShortLastBlock(t *testing.T) {
	tail := []byte("TAIL")
	newData := concat(blkA, blkB, tail) // 20 bytes, last block 4 bytes
	oldData := concat([]byte("junk-"), tail)

	plan := planFor(t, newData, oldData)
	last := plan.Ops[len(plan.Ops)-1]
	if last.Length != 4 {
		t.Fatalf("want last block length 4, got %d", last.Length)
	}
	if !last.Reuse || last.OldOffset != 5 {
		t.Fatalf("want last block reused from old offset 5, got %+v", last)
	}

	dir, cleanup := tempDir(t)
	defer cleanup()
	oldPath := filepath.Join(dir, "old.bin")
	newPath := filepath.Join(dir, "new.bin")
	writeFile(t, oldPath, oldData)

	src := &countingSource{data: newData, block: testBlock}
	if err := Apply(plan, oldPath, newPath, &Options{Source: src}); err != nil {
		t.Fatal(err)
	}
	got, err := ioutil.ReadFile(newPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, newData) {
		t.Fatalf("result mismatch:\n got %q\nwant %q", got, newData)
	}
	if src.fetches != 2 {
		t.Fatalf("want exactly 2 downloads, got %d", src.fetches)
	}
}

// The old file changes after planning, so a planned reuse block no longer
// verifies by content: apply must fall back to downloading that block.
func TestBadReuseBlockFallsBackToDownload(t *testing.T) {
	newData := concat(blkA, blkB)
	oldData := concat(blkA) // block 0 reusable at offset 0

	plan := planFor(t, newData, oldData)
	if !plan.Ops[0].Reuse {
		t.Fatalf("op 0 should be a reuse, got %+v", plan.Ops[0])
	}

	dir, cleanup := tempDir(t)
	defer cleanup()
	oldPath := filepath.Join(dir, "old.bin")
	newPath := filepath.Join(dir, "new.bin")
	// Corrupt the old file after the plan was built.
	writeFile(t, oldPath, []byte("XXXXXXXX"))

	src := &countingSource{data: newData, block: testBlock}
	if err := Apply(plan, oldPath, newPath, &Options{Source: src}); err != nil {
		t.Fatal(err)
	}
	got, err := ioutil.ReadFile(newPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, newData) {
		t.Fatalf("result mismatch:\n got %q\nwant %q", got, newData)
	}
	if src.fetches != 2 {
		t.Fatalf("want 2 downloads (bad reuse fell back), got %d", src.fetches)
	}
}

// Crash mid-apply with the journal persisted: the rerun must resume from
// verified progress and not re-download completed blocks.
func TestResumeAfterCrashWithJournal(t *testing.T) {
	newData := concat(blkA, blkB, blkC, blkD)
	oldData := concat([]byte("ZZ"), blkA, []byte("QQ")) // only block 0 reusable

	dir, cleanup := tempDir(t)
	defer cleanup()
	oldPath := filepath.Join(dir, "old.bin")
	newPath := filepath.Join(dir, "new.bin")
	writeFile(t, oldPath, oldData)
	plan := planFor(t, newData, oldData)

	src := &countingSource{data: newData, block: testBlock}
	crash := errors.New("simulated crash")
	done := 0
	err := Apply(plan, oldPath, newPath, &Options{
		Source: src,
		OnBlockDone: func(index int) error {
			done++
			if done == 3 {
				return crash
			}
			return nil
		},
	})
	if err != crash {
		t.Fatalf("want simulated crash, got %v", err)
	}
	if _, err := os.Stat(newPath + ".journal"); err != nil {
		t.Fatalf("journal should survive the crash: %v", err)
	}
	fetchesBefore := src.fetches

	if err := Apply(plan, oldPath, newPath, &Options{Source: src}); err != nil {
		t.Fatal(err)
	}
	got, err := ioutil.ReadFile(newPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, newData) {
		t.Fatalf("result mismatch:\n got %q\nwant %q", got, newData)
	}
	if src.fetches != 3 {
		t.Fatalf("want 3 total downloads across both runs, got %d", src.fetches)
	}
	if src.fetches == fetchesBefore {
		t.Fatal("second run made no progress")
	}
	if _, err := os.Stat(newPath + ".journal"); !os.IsNotExist(err) {
		t.Fatal("journal should be removed after a successful apply")
	}
}

// Crash after the temp file is fully written, verified and synced, but
// before the journal (progress record) reached disk and before rename.
// The restart must recover purely from temp-file content and still finish.
func TestRestartAfterConfirmWrittenButJournalLost(t *testing.T) {
	newData := concat(blkA, blkB, blkC)

	dir, cleanup := tempDir(t)
	defer cleanup()
	oldPath := filepath.Join(dir, "old.bin")
	newPath := filepath.Join(dir, "new.bin")
	writeFile(t, oldPath, []byte("nothing reusable here"))
	plan := planFor(t, newData, []byte("nothing reusable here"))

	src := &countingSource{data: newData, block: testBlock}
	crash := errors.New("simulated crash after confirm")
	err := Apply(plan, oldPath, newPath, &Options{
		Source:   src,
		NoJournal: true, // progress record never persisted
		BeforeRename: func() error {
			return crash // temp file complete+synced, rename never happened
		},
	})
	if err != crash {
		t.Fatalf("want simulated crash, got %v", err)
	}
	if _, err := os.Stat(newPath + ".journal"); !os.IsNotExist(err) {
		t.Fatal("journal must not exist in this scenario")
	}
	if _, err := os.Stat(newPath); !os.IsNotExist(err) {
		t.Fatal("target must not exist before rename")
	}

	// Restart: no journal, but the temp file content verifies.
	if err := Apply(plan, oldPath, newPath, &Options{Source: src}); err != nil {
		t.Fatal(err)
	}
	got, err := ioutil.ReadFile(newPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, newData) {
		t.Fatalf("result mismatch:\n got %q\nwant %q", got, newData)
	}
	if src.fetches != 3 {
		t.Fatalf("restart re-downloaded blocks: want 3 total fetches, got %d", src.fetches)
	}
}

// If the assembled temp file fails the whole-file digest, the old version
// at the target path must be left untouched.
func TestWholeFileDigestGateProtectsOldVersion(t *testing.T) {
	newData := concat(blkA, blkB)

	dir, cleanup := tempDir(t)
	defer cleanup()
	oldPath := filepath.Join(dir, "old.bin")
	newPath := filepath.Join(dir, "new.bin")
	writeFile(t, oldPath, []byte("irrelevant"))
	plan := planFor(t, newData, nil)

	src := &countingSource{data: newData, block: testBlock}
	err := Apply(plan, oldPath, newPath, &Options{
		Source: src,
		AfterAllBlocks: func(tmpPath string) {
			// Corrupt the assembled file before the digest check.
			f, ferr := os.OpenFile(tmpPath, os.O_WRONLY, 0644)
			if ferr != nil {
				t.Fatal(ferr)
			}
			if _, ferr := f.WriteAt([]byte{0xff}, 0); ferr != nil {
				t.Fatal(ferr)
			}
			f.Close()
		},
	})
	if err == nil {
		t.Fatal("want whole-file digest failure")
	}
	if _, serr := os.Stat(newPath); !os.IsNotExist(serr) {
		t.Fatal("target must not be created when the digest gate fails")
	}
}

func concat(parts ...[]byte) []byte {
	return bytes.Join(parts, nil)
}

func tempDir(t *testing.T) (string, func()) {
	t.Helper()
	dir, err := ioutil.TempDir("", "delta-test")
	if err != nil {
		t.Fatal(err)
	}
	return dir, func() { os.RemoveAll(dir) }
}
