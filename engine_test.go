package hexgrid

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// rectCells 生成 [q0,q1]x[r0,r1] 内全部可通行单元。
func rectCells(q0, q1, r0, r1 int) []Hex {
	var out []Hex
	for q := q0; q <= q1; q++ {
		for r := r0; r <= r1; r++ {
			out = append(out, Hex{q, r})
		}
	}
	return out
}

func cellMap(cells []Hex) map[Hex]bool {
	m := make(map[Hex]bool, len(cells))
	for _, h := range cells {
		m[h] = true
	}
	return m
}

func hexPtr(h Hex) *Hex { return &h }

// assertSameResult 校验分区引擎与不分区参考实现结果完全一致。
func assertSameResult(t *testing.T, got, want Result) {
	t.Helper()
	if !reflect.DeepEqual(got.Reachable, want.Reachable) {
		t.Fatalf("reachable mismatch:\n got %v\nwant %v", got.Reachable, want.Reachable)
	}
	if !reflect.DeepEqual(got.Dist, want.Dist) {
		t.Fatalf("dist mismatch:\n got %v\nwant %v", got.Dist, want.Dist)
	}
	if got.Steps != want.Steps {
		t.Fatalf("steps mismatch: got %d want %d", got.Steps, want.Steps)
	}
	if !reflect.DeepEqual(got.Path, want.Path) {
		t.Fatalf("path mismatch:\n got %v\nwant %v", got.Path, want.Path)
	}
}

func TestNeighborOffsets(t *testing.T) {
	want := [6]Hex{{1, 0}, {1, -1}, {0, -1}, {-1, 0}, {-1, 1}, {0, 1}}
	got := Hex{0, 0}.Neighbors()
	if got != want {
		t.Fatalf("neighbor offsets = %v, want %v", got, want)
	}
}

func TestCrossPartitionMatchesReference(t *testing.T) {
	// 地图横跨多个分区并覆盖负坐标。
	cells := rectCells(-5, 5, -5, 5)
	obstacles := []Hex{{0, 1}, {1, 0}, {2, -1}, {-2, 2}, {-3, 0}, {0, -3}}
	passable := cellMap(cells)
	for _, o := range obstacles {
		passable[o] = false
	}
	var passList []Hex
	for h, p := range passable {
		if p {
			passList = append(passList, h)
		}
	}

	store := NewStore(2, passList) // 小分区，强制跨区
	eng := NewEngine(store)
	start := Hex{-4, 3}
	target := Hex{4, -4}

	got, err := eng.Run(Query{Start: start, K: 8, Target: &target})
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	want, err := ReferenceBFS(passable, start, 8, &target)
	if err != nil {
		t.Fatalf("reference: %v", err)
	}
	assertSameResult(t, got, want)
	if got.Stats.PageLoads < 2 {
		t.Fatalf("expected cross-partition page loads, got %d", got.Stats.PageLoads)
	}
	t.Logf("page loads = %d, reachable = %d, steps = %d", got.Stats.PageLoads, len(got.Reachable), got.Steps)
}

func TestNegativeCoordinates(t *testing.T) {
	cells := rectCells(-4, 0, -4, 0)
	store := NewStore(3, cells)
	eng := NewEngine(store)
	start := Hex{-4, -4}
	target := Hex{-1, -1}

	got, err := eng.Run(Query{Start: start, K: 10, Target: &target})
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	want, err := ReferenceBFS(cellMap(cells), start, 10, &target)
	if err != nil {
		t.Fatalf("reference: %v", err)
	}
	assertSameResult(t, got, want)
	if got.Steps != 6 {
		t.Fatalf("steps = %d, want 6", got.Steps)
	}
}

func TestKZero(t *testing.T) {
	cells := rectCells(-2, 2, -2, 2)
	eng := NewEngine(NewStore(2, cells))
	got, err := eng.Run(Query{Start: Hex{1, -1}, K: 0})
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	if !reflect.DeepEqual(got.Reachable, []Hex{{1, -1}}) {
		t.Fatalf("K=0 reachable = %v, want only start", got.Reachable)
	}
	if got.Dist[Hex{1, -1}] != 0 || len(got.Dist) != 1 {
		t.Fatalf("K=0 dist = %v", got.Dist)
	}
}

func TestObstacleStart(t *testing.T) {
	cells := cellMap(rectCells(-2, 2, -2, 2))
	cells[Hex{0, 0}] = false // 障碍
	var passList []Hex
	for h, p := range cells {
		if p {
			passList = append(passList, h)
		}
	}
	eng := NewEngine(NewStore(2, passList))
	_, err := eng.Run(Query{Start: Hex{0, 0}, K: 3})
	if !errors.Is(err, ErrStartBlocked) {
		t.Fatalf("err = %v, want ErrStartBlocked", err)
	}
	// 起点不在有限地图内同样报错。
	_, err = eng.Run(Query{Start: Hex{99, 99}, K: 3})
	if !errors.Is(err, ErrStartBlocked) {
		t.Fatalf("err = %v, want ErrStartBlocked for unknown cell", err)
	}
}

func TestSurroundedByObstacles(t *testing.T) {
	cells := cellMap(rectCells(-2, 2, -2, 2))
	start := Hex{0, 0}
	for _, d := range Directions {
		cells[start.Add(d)] = false // 六个邻居全部堵死
	}
	var passList []Hex
	for h, p := range cells {
		if p {
			passList = append(passList, h)
		}
	}
	eng := NewEngine(NewStore(2, passList))
	got, err := eng.Run(Query{Start: start, K: 5})
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	if !reflect.DeepEqual(got.Reachable, []Hex{start}) {
		t.Fatalf("surrounded reachable = %v, want only start", got.Reachable)
	}
	want, _ := ReferenceBFS(cells, start, 5, nil)
	assertSameResult(t, got, want)
}

// missingPageLoader 对指定分区注入缺页故障。
type missingPageLoader struct {
	inner PageSource
	bad   PartitionID
}

func (m missingPageLoader) LoadLatest(pid PartitionID) (Page, error) {
	if pid == m.bad {
		return Page{}, ErrPageMissing
	}
	return m.inner.LoadLatest(pid)
}

func (m missingPageLoader) LoadAt(pid PartitionID, v uint64) (Page, error) {
	if pid == m.bad {
		return Page{}, ErrPageMissing
	}
	return m.inner.LoadAt(pid, v)
}

func TestMissingPageFails(t *testing.T) {
	cells := rectCells(-3, 3, -3, 3)
	store := NewStore(2, cells)
	eng := NewEngine(store)
	// 让 (0,0) 所属分区缺页。
	eng.Source = missingPageLoader{inner: store, bad: PartitionOf(Hex{0, 0}, 2)}
	_, err := eng.Run(Query{Start: Hex{0, 0}, K: 3})
	if !errors.Is(err, ErrPageMissing) {
		t.Fatalf("err = %v, want ErrPageMissing", err)
	}
}

func TestVersionConflictRetryThenSucceed(t *testing.T) {
	cells := rectCells(-3, 3, -3, 3)
	store := NewStore(2, cells)
	eng := NewEngine(store)

	// 第一次页加载后并发提交批量更新，模拟异步加载期间的版本漂移。
	fired := false
	store.BeforeLoad = func() {
		if !fired {
			fired = true
			store.BatchUpdate(map[Hex]bool{{1, 1}: false})
		}
	}
	got, err := eng.Run(Query{Start: Hex{0, 0}, K: 4})
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	if got.Stats.Retries != 1 {
		t.Fatalf("retries = %d, want 1", got.Stats.Retries)
	}
	if got.Version.Data != 2 {
		t.Fatalf("version = %+v, want data version 2", got.Version)
	}
	// 结果必须等于新版本上的参考结果，不得是新旧拼合的地图。
	updated := cellMap(cells)
	updated[Hex{1, 1}] = false
	want, _ := ReferenceBFS(updated, Hex{0, 0}, 4, nil)
	assertSameResult(t, got, want)
	if got.Dist[Hex{1, 1}] != 0 {
		t.Fatalf("obstacle cell leaked into result: %v", got.Dist[Hex{1, 1}])
	}
}

func TestVersionConflictReject(t *testing.T) {
	cells := rectCells(-3, 3, -3, 3)
	store := NewStore(2, cells)
	eng := NewEngine(store)
	eng.MaxRetries = 2

	// 每次页加载都换版，永远拿不到一致视图。
	store.BeforeLoad = func() {
		store.BatchUpdate(map[Hex]bool{{0, 0}: true})
	}
	_, err := eng.Run(Query{Start: Hex{0, 0}, K: 4})
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("err = %v, want ErrVersionConflict", err)
	}
}

func TestConfigChangeMidQuery(t *testing.T) {
	cells := rectCells(-3, 3, -3, 3)
	store := NewStore(2, cells)
	eng := NewEngine(store)

	fired := false
	store.BeforeLoad = func() {
		if !fired {
			fired = true
			store.SetChunkSize(3) // 配置换版
		}
	}
	target := Hex{2, -2}
	got, err := eng.Run(Query{Start: Hex{0, 0}, K: 4, Target: &target})
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	if got.Stats.Retries != 1 {
		t.Fatalf("retries = %d, want 1", got.Stats.Retries)
	}
	if got.Version.Config != 2 {
		t.Fatalf("config version = %d, want 2", got.Version.Config)
	}
	// 配置只是存储边界：结果必须与不分区参考一致。
	want, _ := ReferenceBFS(cellMap(cells), Hex{0, 0}, 4, &target)
	assertSameResult(t, got, want)
}

func TestBatchUpdateAndOldVersionQuery(t *testing.T) {
	cells := rectCells(-2, 2, -2, 2)
	store := NewStore(2, cells)
	eng := NewEngine(store)
	start := Hex{0, 0}
	target := Hex{0, 2}

	before, err := eng.Run(Query{Start: start, K: 4, Target: &target})
	if err != nil {
		t.Fatalf("engine: %v", err)
	}

	// 批量更新：堵死 (0,1)，打开原本不可通行的 (3,3)。
	v2 := store.BatchUpdate(map[Hex]bool{{0, 1}: false, {3, 3}: true})

	after, err := eng.Run(Query{Start: start, K: 4, Target: &target})
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	if after.Version != v2 {
		t.Fatalf("version = %+v, want %+v", after.Version, v2)
	}
	updated := cellMap(cells)
	updated[Hex{0, 1}] = false
	updated[Hex{3, 3}] = true
	wantAfter, _ := ReferenceBFS(updated, start, 4, &target)
	assertSameResult(t, after, wantAfter)

	// 旧版本查询：结果必须与更新前完全一致。
	old, err := eng.Run(Query{Start: start, K: 4, Target: &target, Version: &Version{Data: 1}})
	if err != nil {
		t.Fatalf("old version query: %v", err)
	}
	assertSameResult(t, old, before)
	if old.Version.Data != 1 {
		t.Fatalf("old query version = %+v, want data 1", old.Version)
	}
}

// TestRandomizedAgainstReference 随机地图 + 随机查询，引擎必须始终等于参考。
func TestRandomizedAgainstReference(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 50; trial++ {
		cells := rectCells(-4, 4, -4, 4)
		passable := cellMap(cells)
		for h := range passable {
			if rng.Float64() < 0.25 {
				passable[h] = false
			}
		}
		var passList []Hex
		for h, p := range passable {
			if p {
				passList = append(passList, h)
			}
		}
		passable[Hex{0, 0}] = true
		passList = append(passList, Hex{0, 0})

		chunk := 1 + rng.Intn(4)
		eng := NewEngine(NewStore(chunk, passList))
		k := rng.Intn(8)
		target := Hex{rng.Intn(9) - 4, rng.Intn(9) - 4}

		got, err := eng.Run(Query{Start: Hex{0, 0}, K: k, Target: &target})
		if err != nil {
			t.Fatalf("trial %d: engine: %v", trial, err)
		}
		want, err := ReferenceBFS(passable, Hex{0, 0}, k, &target)
		if err != nil {
			t.Fatalf("trial %d: reference: %v", trial, err)
		}
		assertSameResult(t, got, want)
	}
}

// TestLexicographicTieBreak 构造有多条等长最短路径的地图，验证字典序决胜。
func TestLexicographicTieBreak(t *testing.T) {
	cells := rectCells(-3, 3, -3, 3)
	eng := NewEngine(NewStore(2, cells))
	target := Hex{1, 1}
	got, err := eng.Run(Query{Start: Hex{0, 0}, K: 4, Target: &target})
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	if got.Steps != 2 {
		t.Fatalf("steps = %d, want 2", got.Steps)
	}
	// 两条最短路径：(0,0)->(0,1)->(1,1) 与 (0,0)->(1,0)->(1,1)，
	// 字典序决胜选 (0,1) 那条。
	want := []Hex{{0, 0}, {0, 1}, {1, 1}}
	if !reflect.DeepEqual(got.Path, want) {
		t.Fatalf("path = %v, want %v", got.Path, want)
	}
}

func ExampleEngine_Run() {
	cells := rectCells(-2, 2, -2, 2)
	eng := NewEngine(NewStore(2, cells))
	target := Hex{1, 1}
	res, _ := eng.Run(Query{Start: Hex{0, 0}, K: 4, Target: &target})
	fmt.Println("steps:", res.Steps)
	fmt.Println("path:", res.Path)
	// Output:
	// steps: 2
	// path: [0,0 0,1 1,1]
}
