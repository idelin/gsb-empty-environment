package hexgrid

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

// replayFile 镜像 testdata/replay.json 的结构。
type replayFile struct {
	Comment   string `json:"comment"`
	ChunkSize int    `json:"chunkSize"`
	Passable  []Hex  `json:"passable"`
	Obstacles []Hex  `json:"obstacles"`
	Queries   []struct {
		Start  Hex  `json:"start"`
		K      int  `json:"k"`
		Target *Hex `json:"target,omitempty"`
		Expect struct {
			Reachable []Hex `json:"reachable"`
			Steps     int   `json:"steps"`
			Path      []Hex `json:"path,omitempty"`
			PageLoads int   `json:"pageLoads"`
		} `json:"expect"`
	} `json:"queries"`
}

// TestReplayJSON 离线重放 testdata/replay.json：
// 结果必须同时等于文件中的期望与不分区参考实现，并核对访问量。
func TestReplayJSON(t *testing.T) {
	data, err := os.ReadFile("testdata/replay.json")
	if err != nil {
		t.Fatalf("read replay file: %v", err)
	}
	var rf replayFile
	if err := json.Unmarshal(data, &rf); err != nil {
		t.Fatalf("parse replay file: %v", err)
	}

	store := NewStore(rf.ChunkSize, rf.Passable)
	eng := NewEngine(store)

	passable := map[Hex]bool{}
	for _, h := range rf.Passable {
		passable[h] = true
	}
	for _, o := range rf.Obstacles {
		if passable[o] {
			t.Fatalf("obstacle %v listed as passable", o)
		}
		passable[o] = false
	}

	for i, q := range rf.Queries {
		got, err := eng.Run(Query{Start: q.Start, K: q.K, Target: q.Target})
		if err != nil {
			t.Fatalf("query %d: %v", i, err)
		}
		if !reflect.DeepEqual(got.Reachable, q.Expect.Reachable) {
			t.Fatalf("query %d reachable mismatch:\n got %v\nwant %v", i, got.Reachable, q.Expect.Reachable)
		}
		if got.Steps != q.Expect.Steps {
			t.Fatalf("query %d steps = %d, want %d", i, got.Steps, q.Expect.Steps)
		}
		if !reflect.DeepEqual(got.Path, q.Expect.Path) {
			t.Fatalf("query %d path = %v, want %v", i, got.Path, q.Expect.Path)
		}
		if got.Stats.PageLoads != q.Expect.PageLoads {
			t.Fatalf("query %d pageLoads = %d, want %d", i, got.Stats.PageLoads, q.Expect.PageLoads)
		}
		// 交叉验证：与不分区参考实现一致。
		want, err := ReferenceBFS(passable, q.Start, q.K, q.Target)
		if err != nil {
			t.Fatalf("query %d reference: %v", i, err)
		}
		assertSameResult(t, got, want)
		t.Logf("query %d: start=%v k=%d reachable=%d steps=%d pageLoads=%d",
			i, q.Start, q.K, len(got.Reachable), got.Steps, got.Stats.PageLoads)
	}
}
