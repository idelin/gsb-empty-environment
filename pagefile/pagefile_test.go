package pagefile

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func buildFile(t *testing.T, dir string, pageSize uint32, root uint32, pages map[uint32]*Page) string {
	t.Helper()
	max := root
	for no := range pages {
		if no > max {
			max = no
		}
	}
	f := &File{PageSize: pageSize, Root: root, Pages: make([]*Page, max+1)}
	for no, p := range pages {
		f.Pages[no] = p
	}
	path := filepath.Join(dir, "in.bin")
	var buf bytes.Buffer
	if _, err := WriteTo(&buf, f, nil); err != nil {
		t.Fatalf("build: %v", err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func mustCompact(t *testing.T, f *File) (*File, *RelocTable) {
	t.Helper()
	nf, tbl, err := Compact(f)
	if err != nil {
		t.Fatalf("Compact: %v", err)
	}
	if err := ValidateEquivalence(f, nf, tbl); err != nil {
		t.Fatalf("ValidateEquivalence: %v", err)
	}
	return nf, tbl
}

func TestCompactPreservesSharing(t *testing.T) {
	// Diamond: 0 -> {1, 2}, 1 -> 3, 2 -> 3. Page 3 is shared.
	f := &File{PageSize: DefaultPageSize, Root: 0, Pages: []*Page{
		{Refs: []uint32{1, 2}, Payload: []byte("root")},
		{Refs: []uint32{3}, Payload: []byte("left")},
		{Refs: []uint32{3}, Payload: []byte("right")},
		{Payload: []byte("shared")},
	}}
	nf, tbl := mustCompact(t, f)
	if len(nf.Pages) != 4 {
		t.Fatalf("shared subpage was duplicated: got %d pages, want 4", len(nf.Pages))
	}
	m := tbl.Map()
	shared := m[3]
	if nf.Pages[m[1]].Refs[0] != shared || nf.Pages[m[2]].Refs[0] != shared {
		t.Fatalf("sharing broken: left->%d right->%d, shared page is %d",
			nf.Pages[m[1]].Refs[0], nf.Pages[m[2]].Refs[0], shared)
	}
	if got := string(nf.Pages[shared].Payload); got != "shared" {
		t.Fatalf("shared payload = %q", got)
	}
}

func TestCompactSparsePageNumbers(t *testing.T) {
	f := &File{PageSize: DefaultPageSize, Root: 5, Pages: make([]*Page, 12)}
	f.Pages[5] = &Page{ExternalID: 1001, Refs: []uint32{7, 11}, Payload: []byte("root")}
	f.Pages[7] = &Page{ExternalID: 1002, Refs: []uint32{11}, Payload: []byte("mid")}
	f.Pages[11] = &Page{Payload: []byte("leaf")}
	nf, tbl := mustCompact(t, f)
	if len(nf.Pages) != 3 {
		t.Fatalf("got %d pages, want dense 3", len(nf.Pages))
	}
	if nf.Root != tbl.Map()[5] {
		t.Fatalf("new root %d != table mapping %d", nf.Root, tbl.Map()[5])
	}
	if len(tbl.External) != 2 {
		t.Fatalf("external bindings = %d, want 2", len(tbl.External))
	}
	for _, b := range tbl.External {
		if nf.Pages[b.New].ExternalID != b.ExternalID {
			t.Fatalf("external ID %d not on new page %d", b.ExternalID, b.New)
		}
	}
}

func TestCompactRejectsCycles(t *testing.T) {
	cases := map[string][]*Page{
		"self-loop: page references itself": {
			{Refs: []uint32{0}},
		},
		"indirect cycle: 0->1->2->1": {
			{Refs: []uint32{1}},
			{Refs: []uint32{2}},
			{Refs: []uint32{1}},
		},
		"cycle back to root: 0->1->0": {
			{Refs: []uint32{1}},
			{Refs: []uint32{0}},
		},
	}
	for name, pages := range cases {
		t.Run(name, func(t *testing.T) {
			f := &File{PageSize: DefaultPageSize, Root: 0, Pages: pages}
			_, _, err := Compact(f)
			if err == nil || !strings.Contains(err.Error(), "cycle") {
				t.Fatalf("got %v, want cycle error", err)
			}
		})
	}
}

func TestCompactRejectsDanglingRefs(t *testing.T) {
	t.Run("out of range", func(t *testing.T) {
		f := &File{PageSize: DefaultPageSize, Root: 0, Pages: []*Page{
			{Refs: []uint32{9}},
		}}
		if _, _, err := Compact(f); err == nil || !strings.Contains(err.Error(), "dangling") {
			t.Fatalf("got %v, want dangling error", err)
		}
	})
	t.Run("free slot", func(t *testing.T) {
		f := &File{PageSize: DefaultPageSize, Root: 0, Pages: make([]*Page, 3)}
		f.Pages[0] = &Page{Refs: []uint32{2}}
		if _, _, err := Compact(f); err == nil || !strings.Contains(err.Error(), "dangling") {
			t.Fatalf("got %v, want dangling error", err)
		}
	})
	t.Run("dead root", func(t *testing.T) {
		f := &File{PageSize: DefaultPageSize, Root: 1, Pages: make([]*Page, 2)}
		f.Pages[0] = &Page{}
		if _, _, err := Compact(f); err == nil {
			t.Fatal("want error for root on free slot")
		}
	})
}

func TestCompactRejectsDuplicateExternalIDs(t *testing.T) {
	f := &File{PageSize: DefaultPageSize, Root: 0, Pages: []*Page{
		{ExternalID: 42, Refs: []uint32{1}},
		{ExternalID: 42},
	}}
	if _, _, err := Compact(f); err == nil || !strings.Contains(err.Error(), "duplicate external ID") {
		t.Fatalf("got %v, want duplicate external ID error", err)
	}
}

func TestCompactDropsUnreachablePages(t *testing.T) {
	f := &File{PageSize: DefaultPageSize, Root: 0, Pages: []*Page{
		{Refs: []uint32{1}, Payload: []byte("root")},
		{Payload: []byte("live")},
		{Payload: []byte("orphan")},
	}}
	nf, tbl := mustCompact(t, f)
	if len(nf.Pages) != 2 || len(tbl.Entries) != 2 {
		t.Fatalf("unreachable page not dropped: %d pages, %d entries", len(nf.Pages), len(tbl.Entries))
	}
}

func TestValidateDetectsTampering(t *testing.T) {
	newFile := func() (*File, *File, *RelocTable) {
		old := &File{PageSize: DefaultPageSize, Root: 0, Pages: []*Page{
			{ExternalID: 7, Refs: []uint32{1}, Payload: []byte("root")},
			{Payload: []byte("leaf")},
		}}
		nf, tbl, err := Compact(old)
		if err != nil {
			t.Fatal(err)
		}
		return old, nf, tbl
	}
	t.Run("payload mismatch", func(t *testing.T) {
		old, nf, tbl := newFile()
		nf.Pages[1].Payload = []byte("forged")
		if err := ValidateEquivalence(old, nf, tbl); err == nil {
			t.Fatal("tampered payload accepted")
		}
	})
	t.Run("ref mismatch", func(t *testing.T) {
		old, nf, tbl := newFile()
		nf.Pages[0].Refs[0] = 0
		if err := ValidateEquivalence(old, nf, tbl); err == nil {
			t.Fatal("tampered ref accepted")
		}
	})
	t.Run("external ID changed", func(t *testing.T) {
		old, nf, tbl := newFile()
		nf.Pages[0].ExternalID = 99
		if err := ValidateEquivalence(old, nf, tbl); err == nil {
			t.Fatal("changed external ID accepted")
		}
	})
	t.Run("table entry missing", func(t *testing.T) {
		old, nf, tbl := newFile()
		tbl.Entries = tbl.Entries[:1]
		if err := ValidateEquivalence(old, nf, tbl); err == nil {
			t.Fatal("incomplete table accepted")
		}
	})
	t.Run("dangling ref in new file", func(t *testing.T) {
		old, nf, tbl := newFile()
		nf.Pages[0].Refs[0] = 42
		if err := ValidateEquivalence(old, nf, tbl); err == nil {
			t.Fatal("dangling ref accepted")
		}
	})
	t.Run("free slot in new file", func(t *testing.T) {
		old, nf, tbl := newFile()
		nf.Pages[1] = nil
		if err := ValidateEquivalence(old, nf, tbl); err == nil {
			t.Fatal("free slot accepted")
		}
	})
}

func fileHash(t *testing.T, path string) [32]byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(raw)
}

func TestCompactFilePublishesAfterValidation(t *testing.T) {
	dir := t.TempDir()
	in := buildFile(t, dir, DefaultPageSize, 3, map[uint32]*Page{
		3: {ExternalID: 9001, Refs: []uint32{4, 8}, Payload: []byte("root")},
		4: {Refs: []uint32{8}, Payload: []byte("mid")},
		8: {ExternalID: 9002, Payload: []byte("shared leaf")},
	})
	out := filepath.Join(dir, "out.bin")
	tablePath := filepath.Join(dir, "reloc.json")
	before := fileHash(t, in)

	var stages []string
	tbl, err := CompactFile(in, out, tablePath, func(e Event) error {
		stages = append(stages, e.Stage.String())
		return nil
	})
	if err != nil {
		t.Fatalf("CompactFile: %v", err)
	}

	if fileHash(t, in) != before {
		t.Fatal("input file was modified")
	}
	nf, err := readFile(out)
	if err != nil {
		t.Fatalf("read published file: %v", err)
	}
	if len(nf.Pages) != 3 {
		t.Fatalf("published file has %d pages, want 3 (sharing preserved)", len(nf.Pages))
	}
	// External IDs resolve to the same content through the table.
	wantPayload := map[uint64]string{9001: "root", 9002: "shared leaf"}
	for _, b := range tbl.External {
		if got := string(nf.Pages[b.New].Payload); got != wantPayload[b.ExternalID] {
			t.Fatalf("external %d payload = %q", b.ExternalID, got)
		}
	}
	// Table on disk is valid JSON and matches.
	raw, err := os.ReadFile(tablePath)
	if err != nil {
		t.Fatal(err)
	}
	var diskTbl RelocTable
	if err := json.Unmarshal(raw, &diskTbl); err != nil {
		t.Fatalf("table not valid JSON: %v", err)
	}
	if diskTbl.RootOld != 3 || diskTbl.RootNew != nf.Root {
		t.Fatalf("table roots = %d->%d, want 3->%d", diskTbl.RootOld, diskTbl.RootNew, nf.Root)
	}
	// No temp files left behind.
	for _, p := range []string{out + ".tmp", tablePath + ".tmp"} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("temp file %s left behind", p)
		}
	}
	if got := stages[len(stages)-1]; got != "done" {
		t.Fatalf("last stage = %q, want done", got)
	}
}

func TestCompactFileCrashMidWrite(t *testing.T) {
	dir := t.TempDir()
	in := buildFile(t, dir, 128, 0, map[uint32]*Page{
		0: {ExternalID: 1, Refs: []uint32{1, 2}, Payload: []byte("root")},
		1: {Refs: []uint32{2}, Payload: []byte("mid")},
		2: {Payload: []byte("leaf")},
	})
	before := fileHash(t, in)

	// Simulate exiting at byte offsets spanning the whole temp write
	// (total size is 32-byte header + 3 pages x 128 bytes = 416).
	for _, failAt := range []int64{0, 1, 32, 100, 200, 415, 416} {
		out := filepath.Join(dir, fmt.Sprintf("out-%d.bin", failAt))
		tablePath := filepath.Join(dir, fmt.Sprintf("reloc-%d.json", failAt))
		crash := fmt.Errorf("simulated crash at offset %d", failAt)
		_, err := CompactFile(in, out, tablePath, func(e Event) error {
			if e.Stage == StageWriteDataTemp && e.Offset >= failAt {
				return crash
			}
			return nil
		})
		if err == nil {
			t.Fatalf("failAt=%d: expected crash error", failAt)
		}
		for _, p := range []string{out, tablePath, out + ".tmp", tablePath + ".tmp"} {
			if _, serr := os.Stat(p); !os.IsNotExist(serr) {
				t.Fatalf("failAt=%d: %s exists after crash", failAt, p)
			}
		}
		if fileHash(t, in) != before {
			t.Fatalf("failAt=%d: input modified", failAt)
		}
	}
}

func TestCompactFileCrashAtEveryStage(t *testing.T) {
	dir := t.TempDir()
	in := buildFile(t, dir, DefaultPageSize, 0, map[uint32]*Page{
		0: {Refs: []uint32{1}, Payload: []byte("root")},
		1: {Payload: []byte("leaf")},
	})
	for _, stage := range []Stage{StageWriteDataTemp, StageWriteTableTemp, StageValidate, StagePublish} {
		out := filepath.Join(dir, "out.bin")
		tablePath := filepath.Join(dir, "reloc.json")
		crash := fmt.Errorf("simulated crash at %s", stage)
		_, err := CompactFile(in, out, tablePath, func(e Event) error {
			if e.Stage == stage {
				return crash
			}
			return nil
		})
		if err == nil {
			t.Fatalf("stage %s: expected crash error", stage)
		}
		for _, p := range []string{out, tablePath, out + ".tmp", tablePath + ".tmp"} {
			if _, serr := os.Stat(p); !os.IsNotExist(serr) {
				t.Fatalf("stage %s: %s exists after crash", stage, p)
			}
		}
	}
}

func TestCompactFileOverwritesStaleTemps(t *testing.T) {
	dir := t.TempDir()
	in := buildFile(t, dir, DefaultPageSize, 0, map[uint32]*Page{
		0: {Payload: []byte("root")},
	})
	out := filepath.Join(dir, "out.bin")
	tablePath := filepath.Join(dir, "reloc.json")
	// Leftovers from a previously crashed run must not break a new run.
	os.WriteFile(out+".tmp", []byte("garbage"), 0o644)
	os.WriteFile(tablePath+".tmp", []byte("garbage"), 0o644)
	if _, err := CompactFile(in, out, tablePath, nil); err != nil {
		t.Fatalf("CompactFile with stale temps: %v", err)
	}
	if _, err := readFile(out); err != nil {
		t.Fatalf("published file unreadable: %v", err)
	}
}

func TestCompactFileRejectsBadGraphWithoutPublishing(t *testing.T) {
	dir := t.TempDir()
	in := buildFile(t, dir, DefaultPageSize, 0, map[uint32]*Page{
		0: {Refs: []uint32{1}},
		1: {Refs: []uint32{0}}, // cycle
	})
	out := filepath.Join(dir, "out.bin")
	tablePath := filepath.Join(dir, "reloc.json")
	if _, err := CompactFile(in, out, tablePath, nil); err == nil {
		t.Fatal("cyclic graph accepted")
	}
	for _, p := range []string{out, tablePath} {
		if _, serr := os.Stat(p); !os.IsNotExist(serr) {
			t.Fatalf("%s published for bad graph", p)
		}
	}
}
