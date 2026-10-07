package exporter

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func tempDir(t *testing.T) string {
	dir, err := ioutil.TempDir("", "exporter-test-")
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func fillSource(n int) *MemSource {
	src := NewMemSource()
	for i := 0; i < n; i++ {
		src.Set(fmt.Sprintf("key-%04d", i), fmt.Sprintf("value-%d", i))
	}
	return src
}

func wantRecords(n int) []Record {
	var out []Record
	for i := 0; i < n; i++ {
		out = append(out, Record{Key: fmt.Sprintf("key-%04d", i), Value: fmt.Sprintf("value-%d", i)})
	}
	return out
}

func checkSet(t *testing.T, dir string, want []Record) {
	t.Helper()
	got, err := Verify(dir)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("set mismatch:\n got %v\nwant %v", got, want)
	}
}

func TestFullExportThenResumeReusesEverything(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	src := fillSource(25)

	st, err := Export(src, dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	if st.PagesWritten != 3 || st.PagesReused != 0 || st.Total != 25 {
		t.Fatalf("first run stats: %+v", st)
	}
	checkSet(t, dir, wantRecords(25))

	st, err = Export(src, dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	if st.PagesWritten != 0 || st.PagesReused != 3 {
		t.Fatalf("resume should reuse all pages, got %+v", st)
	}
	checkSet(t, dir, wantRecords(25))
}

func TestSourceUpdateForcesFullReexport(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	src := fillSource(25)

	if _, err := Export(src, dir, 10); err != nil {
		t.Fatal(err)
	}

	// Source moves on: old snapshot is no longer valid for reads, and the
	// old manifest must not be reused even though page files look intact.
	src.Set("key-0000", "updated")
	src.Set("key-0025", "new")

	st, err := Export(src, dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	if st.PagesReused != 0 {
		t.Fatalf("snapshot changed: no page may be reused, got %+v", st)
	}
	if st.PagesWritten != 3 || st.Total != 26 {
		t.Fatalf("expected full re-export of 26 records, got %+v", st)
	}
	want := wantRecords(26)
	want[0].Value = "updated"
	want[25].Value = "new"
	checkSet(t, dir, want)
}

func TestCorruptPageFileIsReexported(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	src := fillSource(25)

	if _, err := Export(src, dir, 10); err != nil {
		t.Fatal(err)
	}

	// Corrupt the middle page on disk; offsets and counts still look fine,
	// so only digest verification can catch this.
	victim := filepath.Join(dir, pageFileName(1))
	data, err := ioutil.ReadFile(victim)
	if err != nil {
		t.Fatal(err)
	}
	data[5] ^= 0xff
	if err := ioutil.WriteFile(victim, data, 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := Verify(dir); err == nil {
		t.Fatal("Verify must reject the corrupted page")
	}

	st, err := Export(src, dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	if st.PagesWritten != 1 || st.PagesReused != 2 {
		t.Fatalf("only the corrupt page should be re-exported, got %+v", st)
	}
	checkSet(t, dir, wantRecords(25))
}

func TestMissingPageFileIsReexported(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	src := fillSource(25)

	if _, err := Export(src, dir, 10); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, pageFileName(2))); err != nil {
		t.Fatal(err)
	}

	st, err := Export(src, dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	if st.PagesWritten != 1 || st.PagesReused != 2 {
		t.Fatalf("only the missing page should be re-exported, got %+v", st)
	}
	checkSet(t, dir, wantRecords(25))
}

func TestLyingManifestIsNotTrusted(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	src := fillSource(25)

	if _, err := Export(src, dir, 10); err != nil {
		t.Fatal(err)
	}

	// Rewrite the manifest so page 0 claims a bogus range/count. The digest
	// of the real file no longer matches the entry's story, so the exporter
	// must re-export rather than trust the recorded offset.
	m, err := loadManifest(dir)
	if err != nil || m == nil {
		t.Fatalf("load manifest: %v", err)
	}
	m.Pages[0].Offset = 3
	m.Pages[0].Count = 7
	if err := m.save(dir); err != nil {
		t.Fatal(err)
	}

	st, err := Export(src, dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	if st.PagesWritten != 1 || st.PagesReused != 2 {
		t.Fatalf("forged page entry must be re-exported, got %+v", st)
	}
	checkSet(t, dir, wantRecords(25))
}

func TestRepeatedRecoveryConverges(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	src := fillSource(25)
	want := wantRecords(25)

	if _, err := Export(src, dir, 10); err != nil {
		t.Fatal(err)
	}

	// Repeatedly damage the export in different ways and recover; every
	// cycle must converge back to the same complete set.
	for round := 0; round < 3; round++ {
		switch round {
		case 0:
			os.Remove(filepath.Join(dir, pageFileName(0)))
			p := filepath.Join(dir, pageFileName(2))
			d, _ := ioutil.ReadFile(p)
			ioutil.WriteFile(p, append(d, 'x'), 0644)
		case 1:
			os.Remove(filepath.Join(dir, manifestName))
		case 2:
			ioutil.WriteFile(filepath.Join(dir, pageFileName(1)), []byte("garbage"), 0644)
		}
		st, err := Export(src, dir, 10)
		if err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
		if st.Total != 25 {
			t.Fatalf("round %d: %+v", round, st)
		}
		checkSet(t, dir, want)
	}

	// A clean resume after recovery reuses everything.
	st, err := Export(src, dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	if st.PagesWritten != 0 || st.PagesReused != 3 {
		t.Fatalf("final resume should be a no-op, got %+v", st)
	}
	checkSet(t, dir, want)
}

func TestShrinkRemovesStalePages(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)
	src := fillSource(25)
	if _, err := Export(src, dir, 10); err != nil {
		t.Fatal(err)
	}

	// Source shrinks to a single page; stale page files from the longer
	// export must not survive.
	fresh := NewMemSource()
	for i := 0; i < 5; i++ {
		fresh.Set(fmt.Sprintf("key-%04d", i), fmt.Sprintf("value-%d", i))
	}
	st, err := Export(fresh, dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	if st.PagesWritten != 1 || st.Total != 5 {
		t.Fatalf("stats: %+v", st)
	}
	checkSet(t, dir, wantRecords(5))
	for i := 1; i < 3; i++ {
		if _, err := os.Stat(filepath.Join(dir, pageFileName(i))); !os.IsNotExist(err) {
			t.Fatalf("stale %s should be removed", pageFileName(i))
		}
	}
}
