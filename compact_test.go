package pagereloc

import (
	"bytes"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tempDir(t *testing.T) string {
	t.Helper()
	dir, err := ioutil.TempDir("", "pagereloc-test")
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeSource(t *testing.T, path string, f *File) {
	t.Helper()
	if err := writeFileSync(path, f, 0); err != nil {
		t.Fatal(err)
	}
}

func fileBytes(t *testing.T, path string) []byte {
	t.Helper()
	b, err := ioutil.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestCompactSparseShared covers sparse page numbers, shared subpages,
// unreachable garbage pages, and external object ID preservation.
func TestCompactSparseShared(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	src := filepath.Join(dir, "src.pg")
	dst := filepath.Join(dir, "dst.pg")

	// Sparse graph: root 7 -> {3, 9}; 3 -> 42; 9 -> 42 (shared subpage).
	// Page 20 is allocated but unreachable garbage and must be dropped.
	writeSource(t, src, &File{
		PageSize: DefaultPageSize,
		Root:     7,
		Pages: map[uint32]*Page{
			7:  {ObjectID: 1007, Payload: []byte("root"), Refs: []uint32{3, 9}},
			3:  {ObjectID: 1003, Payload: []byte("left"), Refs: []uint32{42}},
			9:  {ObjectID: 1009, Payload: []byte("right"), Refs: []uint32{42}},
			42: {ObjectID: 1042, Payload: []byte("shared-leaf")},
			20: {ObjectID: 1020, Payload: []byte("garbage")},
		},
	})

	table, err := Compact(src, dst, nil)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}

	// Dense numbering over exactly the 4 reachable pages.
	if len(table.OldToNew) != 4 {
		t.Fatalf("table has %d entries, want 4: %v", len(table.OldToNew), table.OldToNew)
	}
	if _, dropped := table.OldToNew[20]; dropped {
		t.Fatalf("unreachable page 20 should have been dropped: %v", table.OldToNew)
	}
	seen := map[uint32]bool{}
	for _, n := range table.OldToNew {
		if n > 3 || seen[n] {
			t.Fatalf("new numbers not dense/unique: %v", table.OldToNew)
		}
		seen[n] = true
	}

	// Sharing preserved: old parents 3 and 9 must reference the same new
	// page for old 42, and that page must appear exactly once.
	out, err := ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Pages) != 4 {
		t.Fatalf("compacted file has %d pages, want 4", len(out.Pages))
	}
	left := out.Pages[table.OldToNew[3]]
	right := out.Pages[table.OldToNew[9]]
	if left.Refs[0] != right.Refs[0] || left.Refs[0] != table.OldToNew[42] {
		t.Fatalf("sharing broken: left->%d right->%d table[42]=%d",
			left.Refs[0], right.Refs[0], table.OldToNew[42])
	}

	// External object IDs and payloads survive relocation.
	for old, id := range map[uint32]uint64{7: 1007, 3: 1003, 9: 1009, 42: 1042} {
		p := out.Pages[table.OldToNew[old]]
		if p.ObjectID != id {
			t.Fatalf("old page %d: object ID %d, want %d", old, p.ObjectID, id)
		}
	}
	if got := out.Pages[table.OldToNew[42]].Payload; string(got) != "shared-leaf" {
		t.Fatalf("payload corrupted: %q", got)
	}

	// Relocation table published on disk and consistent.
	relocBytes, err := ioutil.ReadFile(dst + ".reloc")
	if err != nil {
		t.Fatalf("relocation table not published: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(relocBytes)), "\n")
	if len(lines) != 5 { // header + 4 entries
		t.Fatalf("reloc table has %d lines, want 5: %q", len(lines), relocBytes)
	}
	if !strings.Contains(string(relocBytes), "42 ") {
		t.Fatalf("reloc table missing entry for old page 42: %q", relocBytes)
	}

	// Independent validation agrees.
	if err := ValidateEquivalence(src, dst, table); err != nil {
		t.Fatalf("ValidateEquivalence: %v", err)
	}
}

// TestCycleRejected rejects cyclic corrupt graphs without publishing.
func TestCycleRejected(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	src := filepath.Join(dir, "src.pg")
	dst := filepath.Join(dir, "dst.pg")

	writeSource(t, src, &File{
		PageSize: DefaultPageSize,
		Root:     0,
		Pages: map[uint32]*Page{
			0: {ObjectID: 1, Refs: []uint32{1}},
			1: {ObjectID: 2, Refs: []uint32{0}}, // cycle back to root
		},
	})

	before := fileBytes(t, src)
	if _, err := Compact(src, dst, nil); err == nil ||
		!strings.Contains(err.Error(), "cycle") {
		t.Fatalf("expected cycle error, got %v", err)
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Fatalf("dst must not be published on failure")
	}
	if !bytes.Equal(fileBytes(t, src), before) {
		t.Fatal("source file modified")
	}
}

// TestDanglingRejected rejects references to unallocated pages.
func TestDanglingRejected(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	src := filepath.Join(dir, "src.pg")
	dst := filepath.Join(dir, "dst.pg")

	writeSource(t, src, &File{
		PageSize: DefaultPageSize,
		Root:     0,
		Pages: map[uint32]*Page{
			0: {ObjectID: 1, Refs: []uint32{5}}, // page 5 does not exist
		},
	})

	if _, err := Compact(src, dst, nil); err == nil ||
		!strings.Contains(err.Error(), "dangling") {
		t.Fatalf("expected dangling reference error, got %v", err)
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Fatal("dst must not be published on failure")
	}
}

// TestCrashMidWrite simulates an abrupt exit partway through writing the
// new file: nothing is published, the source is intact, and a later run
// recovers and succeeds.
func TestCrashMidWrite(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	src := filepath.Join(dir, "src.pg")
	dst := filepath.Join(dir, "dst.pg")

	writeSource(t, src, &File{
		PageSize: DefaultPageSize,
		Root:     0,
		Pages: map[uint32]*Page{
			0: {ObjectID: 1, Payload: []byte("a"), Refs: []uint32{1, 2}},
			1: {ObjectID: 2, Payload: []byte("b"), Refs: []uint32{3}},
			2: {ObjectID: 3, Payload: []byte("c"), Refs: []uint32{3}},
			3: {ObjectID: 4, Payload: []byte("d")},
		},
	})
	before := fileBytes(t, src)

	_, err := Compact(src, dst, &Options{CrashAfterPages: 2})
	if err == nil {
		t.Fatal("expected crash error")
	}
	if _, statErr := os.Stat(dst); !os.IsNotExist(statErr) {
		t.Fatal("crashed write must not publish dst")
	}
	if _, statErr := os.Stat(dst + ".reloc"); !os.IsNotExist(statErr) {
		t.Fatal("crashed write must not publish reloc table")
	}
	if !bytes.Equal(fileBytes(t, src), before) {
		t.Fatal("source file modified by crashed run")
	}
	// A partial temp file may remain, exactly like after a power loss.
	tmpInfo, statErr := os.Stat(dst + ".tmp")
	if statErr != nil {
		t.Fatalf("expected partial temp file to remain: %v", statErr)
	}
	full := int64(headerSize + 4*DefaultPageSize)
	if tmpInfo.Size() >= full {
		t.Fatalf("temp file size %d, want a partial write < %d", tmpInfo.Size(), full)
	}

	// Recovery: a fresh run overwrites the stale temp and succeeds.
	table, err := Compact(src, dst, nil)
	if err != nil {
		t.Fatalf("recovery Compact: %v", err)
	}
	if err := ValidateEquivalence(src, dst, table); err != nil {
		t.Fatalf("ValidateEquivalence after recovery: %v", err)
	}
	if _, statErr := os.Stat(dst + ".tmp"); !os.IsNotExist(statErr) {
		t.Fatal("temp file should be consumed by rename after success")
	}
}

// TestValidationCatchesTampering confirms the independent traversal
// rejects a compacted file whose content no longer matches the source.
func TestValidationCatchesTampering(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	src := filepath.Join(dir, "src.pg")
	dst := filepath.Join(dir, "dst.pg")

	writeSource(t, src, &File{
		PageSize: DefaultPageSize,
		Root:     0,
		Pages: map[uint32]*Page{
			0: {ObjectID: 1, Payload: []byte("root"), Refs: []uint32{1}},
			1: {ObjectID: 2, Payload: []byte("leaf")},
		},
	})
	table, err := Compact(src, dst, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Tamper: rewrite the leaf's object ID in the published file.
	out, err := ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	out.Pages[table.OldToNew[1]].ObjectID = 999
	if err := writeFileSync(dst, out, 0); err != nil {
		t.Fatal(err)
	}
	if err := ValidateEquivalence(src, dst, table); err == nil ||
		!strings.Contains(err.Error(), "object ID") {
		t.Fatalf("expected object ID mismatch, got %v", err)
	}
}
