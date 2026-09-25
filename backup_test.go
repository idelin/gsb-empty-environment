package pagebackup

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain cleans up temp dirs left by the tests (t.Cleanup does not
// exist in Go 1.11).
func TestMain(m *testing.M) {
	code := m.Run()
	matches, _ := filepath.Glob(filepath.Join(os.TempDir(), "pagebackup-test-*"))
	for _, dir := range matches {
		os.RemoveAll(dir)
	}
	os.Exit(code)
}

func tempDir(t *testing.T) string {
	dir, err := ioutil.TempDir("", "pagebackup-test-")
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func makePage(seed byte) []byte {
	p := make([]byte, PageSize)
	for i := range p {
		p[i] = seed + byte(i)
	}
	return p
}

// fixture builds a source DB with 3 versions and exports:
//
//	full/   full backup at v1
//	inc1/   incremental v1 -> v2
//	inc2/   incremental v2 -> v3
type fixture struct {
	root      string
	src       *DB
	srcDigest string
	fullDir   string
	inc1Dir   string
	inc2Dir   string
}

func newFixture(t *testing.T) *fixture {
	root := tempDir(t)
	db, err := CreateDB(filepath.Join(root, "src"))
	if err != nil {
		t.Fatal(err)
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(db.ApplyChanges(map[uint32][]byte{1: makePage(0x10), 2: makePage(0x20), 3: makePage(0x30)})) // v1
	fullDir := filepath.Join(root, "backups", "full-v1")
	must(ExportFull(db, fullDir))

	must(db.ApplyChanges(map[uint32][]byte{2: makePage(0x21), 4: makePage(0x40)})) // v2
	inc1Dir := filepath.Join(root, "backups", "incr-v1-v2")
	must(ExportIncremental(db, inc1Dir, 1))

	must(db.ApplyChanges(map[uint32][]byte{3: makePage(0x31)})) // v3
	inc2Dir := filepath.Join(root, "backups", "incr-v2-v3")
	must(ExportIncremental(db, inc2Dir, 2))

	digest, err := db.Digest()
	must(err)
	return &fixture{root: root, src: db, srcDigest: digest, fullDir: fullDir, inc1Dir: inc1Dir, inc2Dir: inc2Dir}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func restoreAndDigest(t *testing.T, chain []string, target string) (uint64, string) {
	t.Helper()
	if err := Restore(chain, target); err != nil {
		t.Fatalf("restore: %v", err)
	}
	db, err := OpenDB(target)
	if err != nil {
		t.Fatalf("open restored db: %v", err)
	}
	digest, err := db.Digest()
	if err != nil {
		t.Fatalf("digest restored db: %v", err)
	}
	return db.Version(), digest
}

func TestFullRestoreDigestEqualsSource(t *testing.T) {
	f := newFixture(t)
	// Restore only the full backup: result must equal the source at v1.
	target := filepath.Join(f.root, "restore-full")
	if err := Restore([]string{f.fullDir}, target); err != nil {
		t.Fatalf("restore: %v", err)
	}
	db, err := OpenDB(target)
	if err != nil {
		t.Fatal(err)
	}
	if db.Version() != 1 {
		t.Fatalf("version = %d, want 1", db.Version())
	}
	m, err := loadManifest(f.fullDir)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := db.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if digest != m.Digest {
		t.Fatalf("restored digest %s != manifest digest %s", digest, m.Digest)
	}
}

func TestIncrementalChainRestoreDigestEqualsSource(t *testing.T) {
	f := newFixture(t)
	target := filepath.Join(f.root, "restore-chain")
	version, digest := restoreAndDigest(t, []string{f.fullDir, f.inc1Dir, f.inc2Dir}, target)
	if version != 3 {
		t.Fatalf("version = %d, want 3", version)
	}
	if digest != f.srcDigest {
		t.Fatalf("restored digest %s != source digest %s", digest, f.srcDigest)
	}
}

func TestGapRejectedAndNoPartialDB(t *testing.T) {
	f := newFixture(t)
	// inc2 (base=2) must not apply directly onto full (v1), skipping inc1.
	target := filepath.Join(f.root, "restore-gap")
	err := Restore([]string{f.fullDir, f.inc2Dir}, target)
	if err == nil {
		t.Fatal("expected error for skipped increment, got nil")
	}
	if !strings.Contains(err.Error(), "broken chain") {
		t.Fatalf("unexpected error: %v", err)
	}
	if exists(target) {
		t.Fatal("partial database left behind after rejected restore")
	}
}

func TestMissingLinkRejected(t *testing.T) {
	f := newFixture(t)
	target := filepath.Join(f.root, "restore-missing")
	// Chain jumps straight to an incremental whose base (1) matches the
	// full backup, but the chain omits the full backup itself.
	err := Restore([]string{f.inc1Dir, f.inc2Dir}, target)
	if err == nil {
		t.Fatal("expected error for chain without full backup, got nil")
	}
	if exists(target) {
		t.Fatal("partial database left behind after rejected restore")
	}
}

func TestIdempotentReapply(t *testing.T) {
	f := newFixture(t)
	target := filepath.Join(f.root, "restore-idem")
	// Applying inc2 twice must be a no-op, not an error.
	version, digest := restoreAndDigest(t, []string{f.fullDir, f.inc1Dir, f.inc2Dir, f.inc2Dir}, target)
	if version != 3 {
		t.Fatalf("version = %d, want 3", version)
	}
	if digest != f.srcDigest {
		t.Fatalf("restored digest %s != source digest %s", digest, f.srcDigest)
	}
}

func TestCorruptedPageRejected(t *testing.T) {
	f := newFixture(t)
	// Flip one byte in a page payload of inc2.
	pageFile := filepath.Join(f.inc2Dir, "pages", "00000003.pg")
	data, err := ioutil.ReadFile(pageFile)
	if err != nil {
		t.Fatal(err)
	}
	data[0] ^= 0xff
	if err := ioutil.WriteFile(pageFile, data, 0644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(f.root, "restore-corrupt")
	err = Restore([]string{f.fullDir, f.inc1Dir, f.inc2Dir}, target)
	if err == nil {
		t.Fatal("expected corruption error, got nil")
	}
	if !strings.Contains(err.Error(), "corrupt") {
		t.Fatalf("unexpected error: %v", err)
	}
	if exists(target) {
		t.Fatal("partial database left behind after corrupted restore")
	}
}

func TestCorruptedManifestRejected(t *testing.T) {
	f := newFixture(t)
	// Tamper with the manifest of inc1: change the target version.
	manifestFile := filepath.Join(f.inc1Dir, "manifest.json")
	data, err := ioutil.ReadFile(manifestFile)
	if err != nil {
		t.Fatal(err)
	}
	tampered := strings.Replace(string(data), `"target": 2`, `"target": 7`, 1)
	if tampered == string(data) {
		t.Fatal("tamper pattern not found in manifest")
	}
	if err := ioutil.WriteFile(manifestFile, []byte(tampered), 0644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(f.root, "restore-tampered")
	err = Restore([]string{f.fullDir, f.inc1Dir, f.inc2Dir}, target)
	if err == nil {
		t.Fatal("expected manifest checksum error, got nil")
	}
	if !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("unexpected error: %v", err)
	}
	if exists(target) {
		t.Fatal("partial database left behind after tampered restore")
	}
}

func TestFaultInjectedDuringExport(t *testing.T) {
	f := newFixture(t)
	defer func() { FaultInject = nil }()
	FaultInject = func(p FaultPoint) error {
		if p == PointExportPagesWritten {
			return fmt.Errorf("injected disk fault")
		}
		return nil
	}
	dir := filepath.Join(f.root, "backups", "incr-fault")
	err := ExportIncremental(f.src, dir, 2)
	if err == nil || !strings.Contains(err.Error(), "injected disk fault") {
		t.Fatalf("expected injected fault, got %v", err)
	}
	if exists(dir) {
		t.Fatal("backup dir exists after aborted export")
	}
	// Staging area must have been cleaned up on the error path.
	matches, _ := filepath.Glob(dir + ".staging.*")
	if len(matches) != 0 {
		t.Fatalf("staging dirs left behind: %v", matches)
	}
}

func TestCrashDuringExportYieldsUnusableBackup(t *testing.T) {
	f := newFixture(t)
	defer func() { CrashInject = nil }()
	CrashInject = func(p FaultPoint) {
		if p == PointExportManifestCommit {
			panic("simulated power loss")
		}
	}
	dir := filepath.Join(f.root, "backups", "incr-crash")
	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Error("expected panic from CrashInject")
			}
		}()
		ExportIncremental(f.src, dir, 2)
	}()
	// The final backup dir must not exist; only a staging dir remains.
	if exists(dir) {
		t.Fatal("backup dir exists after crashed export")
	}
	matches, _ := filepath.Glob(dir + ".staging.*")
	if len(matches) != 1 {
		t.Fatalf("want exactly 1 leftover staging dir, got %v", matches)
	}
	// A chain referencing the crashed backup must fail, not silently
	// restore a partial database.
	target := filepath.Join(f.root, "restore-crashed-backup")
	if err := Restore([]string{f.fullDir, f.inc1Dir, dir}, target); err == nil {
		t.Fatal("expected error restoring from crashed export, got nil")
	}
	if exists(target) {
		t.Fatal("partial database left behind")
	}
}

func TestCrashDuringRestoreLeavesNoPartialAndRecovers(t *testing.T) {
	f := newFixture(t)
	target := filepath.Join(f.root, "restore-crash")

	CrashInject = func(p FaultPoint) {
		if p == PointRestoreBeforeCommit {
			panic("simulated power loss")
		}
	}
	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Error("expected panic from CrashInject")
			}
		}()
		Restore([]string{f.fullDir, f.inc1Dir, f.inc2Dir}, target)
	}()
	CrashInject = nil

	// Crash struck after full verification but before the atomic
	// rename: target must not exist, staging dir must remain.
	if exists(target) {
		t.Fatal("target dir exists after crashed restore")
	}
	matches, _ := filepath.Glob(target + ".staging.*")
	if len(matches) != 1 {
		t.Fatalf("want exactly 1 leftover staging dir, got %v", matches)
	}

	// Next run reclaims the stale staging area and succeeds.
	version, digest := restoreAndDigest(t, []string{f.fullDir, f.inc1Dir, f.inc2Dir}, target)
	if version != 3 || digest != f.srcDigest {
		t.Fatalf("after recovery: version=%d digest=%s, want 3/%s", version, digest, f.srcDigest)
	}
	if matches, _ := filepath.Glob(target + ".staging.*"); len(matches) != 0 {
		t.Fatalf("stale staging dirs not reclaimed: %v", matches)
	}
}

func TestFilesStayInCallerSpecifiedDir(t *testing.T) {
	f := newFixture(t)
	target := filepath.Join(f.root, "restore-placement")
	if _, _ = restoreAndDigest(t, []string{f.fullDir, f.inc1Dir, f.inc2Dir}, target); !exists(filepath.Join(target, "state.json")) {
		t.Fatal("restored state.json missing from caller-specified dir")
	}
	if !exists(filepath.Join(target, "pages", "00000001.pg")) {
		t.Fatal("restored page missing from caller-specified dir")
	}
	// Nothing may leak into the process working directory.
	for _, name := range []string{"state.json", "pages"} {
		if exists(name) {
			t.Fatalf("unexpected artifact in working directory: %s", name)
		}
	}
}
