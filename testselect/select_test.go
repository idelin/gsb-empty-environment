package testselect

import (
	"io/ioutil"
	"os"
	"reflect"
	"testing"
)

func baseSnapshot() *Snapshot {
	return &Snapshot{
		Functions: map[string]string{
			"main":   "v1",
			"handle": "v1",
			"parse":  "v1",
			"store":  "v1",
			"util":   "v1",
		},
		CallGraph: map[string][]string{
			"main":   {"handle"},
			"handle": {"parse", "store"},
			"parse":  {"util"},
		},
		Dynamic: map[string][]string{
			"handle": {"plugin.A"},
		},
		Coverage: map[string][]string{
			"TestMain":   {"main"},
			"TestHandle": {"handle"},
			"TestParse":  {"parse", "util"},
			"TestUtil":   {"util"},
		},
	}
}

func baseCurrent() *Current {
	return &Current{
		Functions: map[string]string{
			"main":   "v1",
			"handle": "v1",
			"parse":  "v1",
			"store":  "v1",
			"util":   "v1",
		},
		CallGraph: map[string][]string{
			"main":   {"handle"},
			"handle": {"parse", "store"},
			"parse":  {"util"},
		},
		Dynamic: map[string][]string{
			"handle": {"plugin.A"},
		},
	}
}

func TestNoChangeSelectsNothing(t *testing.T) {
	sel := Select(baseSnapshot(), baseCurrent())
	if sel.RunAll {
		t.Fatalf("unexpected RunAll: %s", sel.Reason)
	}
	if len(sel.Tests) != 0 {
		t.Fatalf("expected no tests, got %v", sel.Tests)
	}
}

func TestNoSnapshotRunsAll(t *testing.T) {
	sel := Select(nil, baseCurrent())
	if !sel.RunAll {
		t.Fatal("expected RunAll without snapshot")
	}
}

func TestModifiedFunctionSelectsCoveringTests(t *testing.T) {
	cur := baseCurrent()
	cur.Functions["util"] = "v2"
	sel := Select(baseSnapshot(), cur)
	// util 被修改，沿并集图反向传播: util <- parse <- handle <- main。
	want := []string{"TestHandle", "TestMain", "TestParse", "TestUtil"}
	if !reflect.DeepEqual(sel.Tests, want) {
		t.Fatalf("got %v, want %v", sel.Tests, want)
	}
}

func TestModifiedLeafSelectsOnlyCoveringTest(t *testing.T) {
	cur := baseCurrent()
	cur.Functions["store"] = "v2"
	sel := Select(baseSnapshot(), cur)
	// store 无测试直接覆盖，但 handle 是其调用方，main 又调用 handle。
	want := []string{"TestHandle", "TestMain"}
	if !reflect.DeepEqual(sel.Tests, want) {
		t.Fatalf("got %v, want %v", sel.Tests, want)
	}
}

func TestRemovedEdgePropagatesOnUnionGraph(t *testing.T) {
	cur := baseCurrent()
	// 删除静态边 handle -> store。
	cur.CallGraph["handle"] = []string{"parse"}
	sel := Select(baseSnapshot(), cur)
	// 删边使 handle 成为种子，并沿旧图并集传播到 main。
	want := []string{"TestHandle", "TestMain"}
	if !reflect.DeepEqual(sel.Tests, want) {
		t.Fatalf("got %v, want %v", sel.Tests, want)
	}
}

func TestAddedEdgePropagates(t *testing.T) {
	cur := baseCurrent()
	// 新增边 parse -> store。
	cur.CallGraph["parse"] = []string{"util", "store"}
	sel := Select(baseSnapshot(), cur)
	// parse 成为种子，传播到 handle、main。
	want := []string{"TestHandle", "TestMain", "TestParse"}
	if !reflect.DeepEqual(sel.Tests, want) {
		t.Fatalf("got %v, want %v", sel.Tests, want)
	}
}

func TestNewFunctionTriggersRunAll(t *testing.T) {
	cur := baseCurrent()
	cur.Functions["brandNew"] = "v1"
	sel := Select(baseSnapshot(), cur)
	if !sel.RunAll {
		t.Fatal("expected RunAll for function with unknown coverage")
	}
}

func TestDeletedFunctionExpiresCoverage(t *testing.T) {
	cur := baseCurrent()
	// 删除 util 及其入边。
	delete(cur.Functions, "util")
	cur.CallGraph["parse"] = []string{}
	sel := Select(baseSnapshot(), cur)
	// 所有测试的依赖闭包都传递到达已删除的 util，覆盖全部过期。
	wantStale := []string{"TestHandle", "TestMain", "TestParse", "TestUtil"}
	if !reflect.DeepEqual(sel.Stale, wantStale) {
		t.Fatalf("stale: got %v, want %v", sel.Stale, wantStale)
	}
	// 过期测试被选中重跑，且删边沿并集传播到 handle/main。
	for _, test := range []string{"TestParse", "TestUtil", "TestHandle", "TestMain"} {
		if _, ok := sel.Reasons[test]; !ok {
			t.Fatalf("expected %s selected, got %v", test, sel.Tests)
		}
	}
}

func TestDynamicEdgeChangeSelectsCaller(t *testing.T) {
	cur := baseCurrent()
	// 动态分派目标变化：plugin.A -> plugin.B。
	cur.Dynamic["handle"] = []string{"plugin.B"}
	sel := Select(baseSnapshot(), cur)
	// 动态边增删使 handle 成为种子，传播到 main。
	want := []string{"TestHandle", "TestMain"}
	if !reflect.DeepEqual(sel.Tests, want) {
		t.Fatalf("got %v, want %v", sel.Tests, want)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir, err := ioutil.TempDir("", "testselect")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := dir + "/snapshot.json"
	snap := baseSnapshot()
	if err := Save(snap, path); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(snap, loaded) {
		t.Fatalf("round trip mismatch:\n%+v\n%+v", snap, loaded)
	}
	missing, err := Load(path + ".missing")
	if err != nil || missing != nil {
		t.Fatalf("missing file: got %v, %v", missing, err)
	}
}
