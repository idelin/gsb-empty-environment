package mvcc

import (
	"reflect"
	"testing"
)

// 搭建验收场景：数据项 X 有版本 v1(t=1), v2(t=5), v3(t=10)。
func newStoreWithX() *Store {
	s := NewStore()
	s.Write("X", 1, "v1")
	s.Write("X", 5, "v2")
	s.Write("X", 10, "v3")
	return s
}

func mustRead(t *testing.T, s *Store, rv *ReadView, key, wantVal string, wantTS uint64) {
	t.Helper()
	val, ts, ok := s.Read(rv, key)
	if !ok {
		t.Fatalf("Read(rv=%d, %q) 未找到可见版本", rv.TS(), key)
	}
	if val != wantVal || ts != wantTS {
		t.Fatalf("Read(rv=%d, %q) = (%q, t=%d), 期望 (%q, t=%d)",
			rv.TS(), key, val, ts, wantVal, wantTS)
	}
}

// TestReadVisibility 验证 GC 之前各 ReadView 的可见性。
func TestReadVisibility(t *testing.T) {
	s := newStoreWithX()

	cases := []struct {
		readTS  uint64
		wantVal string
		wantTS  uint64
	}{
		{1, "v1", 1},
		{4, "v1", 1},
		{5, "v2", 5},
		{8, "v2", 5},
		{9, "v2", 5},
		{10, "v3", 10},
		{100, "v3", 10},
	}
	for _, c := range cases {
		rv := s.BeginRead(c.readTS)
		mustRead(t, s, rv, "X", c.wantVal, c.wantTS)
		s.EndRead(rv)
	}

	// 读时间戳早于所有版本：不可见。
	rv := s.BeginRead(0)
	if _, _, ok := s.Read(rv, "X"); ok {
		t.Fatal("readTS=0 时不应读到任何版本")
	}
	s.EndRead(rv)
}

// TestGCPrunesCoveredVersions 验收边界：
// 最小活跃 ReadView 为 t=8 时，GC 安全修剪 v1，保留 v2、v3。
func TestGCPrunesCoveredVersions(t *testing.T) {
	s := newStoreWithX()

	// 活跃读事务：t=8（最小）、t=12。
	rv8 := s.BeginRead(8)
	rv12 := s.BeginRead(12)
	defer s.EndRead(rv8)
	defer s.EndRead(rv12)

	minTS, ok := s.MinActiveReadTS()
	if !ok || minTS != 8 {
		t.Fatalf("MinActiveReadTS = (%d, %v), 期望 (8, true)", minTS, ok)
	}

	if n := s.VersionCount("X"); n != 3 {
		t.Fatalf("GC 前版本数 = %d, 期望 3", n)
	}

	pruned := s.GC("X")
	if pruned != 1 {
		t.Fatalf("GC 修剪版本数 = %d, 期望 1", pruned)
	}
	if n := s.VersionCount("X"); n != 2 {
		t.Fatalf("GC 后版本数 = %d, 期望 2", n)
	}
	if got, want := s.VersionTimestamps("X"), []uint64{10, 5}; !reflect.DeepEqual(got, want) {
		t.Fatalf("GC 后剩余版本 = %v, 期望 %v", got, want)
	}

	// GC 后所有只读查询依然正确：t=8 读到 v2，t=12 读到 v3。
	mustRead(t, s, rv8, "X", "v2", 5)
	mustRead(t, s, rv12, "X", "v3", 10)

	// 新读者同样正确。
	rv9 := s.BeginRead(9)
	defer s.EndRead(rv9)
	mustRead(t, s, rv9, "X", "v2", 5)
	rv100 := s.BeginRead(100)
	defer s.EndRead(rv100)
	mustRead(t, s, rv100, "X", "v3", 10)
}

// TestGCRespectsOldestReader 验证 GC 不会修剪最老读者仍可见的版本。
func TestGCRespectsOldestReader(t *testing.T) {
	s := newStoreWithX()

	// 最老读者 ReadView 为 t=3：只能看到 v1，任何版本都不能被修剪。
	rv3 := s.BeginRead(3)
	if pruned := s.GC("X"); pruned != 0 {
		t.Fatalf("最老读者 t=3 时 GC 修剪了 %d 个版本, 期望 0", pruned)
	}
	mustRead(t, s, rv3, "X", "v1", 1)
	s.EndRead(rv3)

	// 释放后最小活跃 ReadView 变为 t=6：v1 被 v2 覆盖，可修剪。
	rv6 := s.BeginRead(6)
	defer s.EndRead(rv6)
	if pruned := s.GC("X"); pruned != 1 {
		t.Fatalf("最老读者 t=6 时 GC 修剪了 %d 个版本, 期望 1", pruned)
	}
	mustRead(t, s, rv6, "X", "v2", 5)
}

// TestGCWithNoActiveReaders 无活跃读者时仅保留最新版本。
func TestGCWithNoActiveReaders(t *testing.T) {
	s := newStoreWithX()

	if pruned := s.GC("X"); pruned != 2 {
		t.Fatalf("无活跃读者时 GC 修剪了 %d 个版本, 期望 2", pruned)
	}
	if got, want := s.VersionTimestamps("X"), []uint64{10}; !reflect.DeepEqual(got, want) {
		t.Fatalf("GC 后剩余版本 = %v, 期望 %v", got, want)
	}

	rv := s.BeginRead(100)
	defer s.EndRead(rv)
	mustRead(t, s, rv, "X", "v3", 10)
}

// TestGCReleasesMemory 验证被修剪的版本确实从链上断开、可被运行时回收。
func TestGCReleasesMemory(t *testing.T) {
	s := newStoreWithX()
	rv := s.BeginRead(8)
	defer s.EndRead(rv)

	s.GC("X")
	// 链上只剩 v3 -> v2，v1 不再可达。
	if got := s.VersionTimestamps("X"); len(got) != 2 {
		t.Fatalf("GC 后链长 = %d, 期望 2", len(got))
	}
}

// TestReadMissingKey 读取不存在的数据项。
func TestReadMissingKey(t *testing.T) {
	s := NewStore()
	rv := s.BeginRead(10)
	defer s.EndRead(rv)
	if _, _, ok := s.Read(rv, "nope"); ok {
		t.Fatal("不存在的 key 不应可读")
	}
	if pruned := s.GC("nope"); pruned != 0 {
		t.Fatalf("对不存在的 key 执行 GC 应返回 0, 实际 %d", pruned)
	}
}
