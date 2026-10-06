package testsel

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func tmpDir(t *testing.T) (string, func()) {
	t.Helper()
	dir, err := ioutil.TempDir("", "testsel-")
	if err != nil {
		t.Fatal(err)
	}
	return dir, func() { os.RemoveAll(dir) }
}

func TestStoreRoundTrip(t *testing.T) {
	s := snap()
	s.Funcs["m.a"] = "h1"
	s.Funcs["m.(T).b"] = "h2"
	s.Edges = []Edge{{"m.a", "m.(T).b"}, {"m.a", "m.(T).b"}}
	s.DynCalls["m.a"] = []string{"Run", "Run"}
	s.DynImpls["Run"] = []FuncID{"m.(T).b"}
	s.Coverage["m.TestA"] = []FuncID{"m.a", "m.(T).b", "m.a"}

	dir, cleanup := tmpDir(t)
	defer cleanup()
	path := filepath.Join(dir, "snap.json")
	if err := SaveSnapshot(path, s); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, s) {
		t.Fatalf("round trip mismatch:\ngot  %+v\nwant %+v", got, s)
	}
	if len(got.Edges) != 1 {
		t.Fatalf("edges not deduplicated: %v", got.Edges)
	}
	if len(got.Coverage["m.TestA"]) != 2 {
		t.Fatalf("coverage not deduplicated: %v", got.Coverage["m.TestA"])
	}
}

func TestLoadSnapshotBadVersion(t *testing.T) {
	dir, cleanup := tmpDir(t)
	defer cleanup()
	path := filepath.Join(dir, "snap.json")
	if err := SaveSnapshot(path, snap()); err != nil {
		t.Fatal(err)
	}
	// Corrupt the version field.
	data := []byte(`{"version": 99, "snapshot": {}}`)
	if err := ioutil.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadSnapshot(path); err == nil {
		t.Fatal("expected version error")
	}
}
