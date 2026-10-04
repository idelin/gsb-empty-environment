package set32

import (
	"math/rand"
	"reflect"
	"testing"
)

func mustCollect(t *testing.T, s *Set, want []uint32) {
	t.Helper()
	got := s.Collect()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Collect = %v, want %v", got, want)
	}
}

// 65535/65536 边界：跨块边界的增删查。
func TestBoundary65535_65536(t *testing.T) {
	s := New()
	for _, v := range []uint32{0, 65534, 65535, 65536, 65537, 4294967295} {
		s.Add(v)
	}
	for _, v := range []uint32{0, 65534, 65535, 65536, 65537, 4294967295} {
		if !s.Contains(v) {
			t.Fatalf("missing %d", v)
		}
	}
	for _, v := range []uint32{1, 65533, 65538, 4294967294} {
		if s.Contains(v) {
			t.Fatalf("unexpected %d", v)
		}
	}
	if got := s.Rank(65535); got != 3 {
		t.Fatalf("Rank(65535) = %d, want 3", got)
	}
	if got := s.Rank(65536); got != 4 {
		t.Fatalf("Rank(65536) = %d, want 4", got)
	}
	if got := s.Rank(4294967295); got != 6 {
		t.Fatalf("Rank(max) = %d, want 6", got)
	}
	v, err := s.Select(3)
	if err != nil || v != 65536 {
		t.Fatalf("Select(3) = %d, %v, want 65536", v, err)
	}
	v, err = s.Select(5)
	if err != nil || v != 4294967295 {
		t.Fatalf("Select(5) = %d, %v, want max", v, err)
	}
	// 删除 65536 后块 1 仍含 65537；删除 65537 后块 1 消失。
	s.Remove(65536)
	if s.Contains(65536) || !s.Contains(65537) {
		t.Fatal("remove 65536 failed")
	}
	s.Remove(65537)
	if len(s.blocks) != 2 { // 块 0 与块 65535
		t.Fatalf("blocks = %d, want 2", len(s.blocks))
	}
	mustCollect(t, s, []uint32{0, 65534, 65535, 4294967295})
}

// 全块：0..65535 填满一个块，应落为单 run 的 runs 容器，编码 24 字节。
func TestFullBlock(t *testing.T) {
	s := New()
	for v := uint32(0); v < 65536; v++ {
		s.Add(v)
	}
	if s.Len() != 65536 {
		t.Fatalf("Len = %d", s.Len())
	}
	if len(s.blocks) != 1 {
		t.Fatalf("blocks = %d, want 1", len(s.blocks))
	}
	if k := s.blocks[0].c.containerKind(); k != kindRuns {
		t.Fatalf("kind = %d, want runs", k)
	}
	if got := s.EncodedSize(); got != 24 {
		t.Fatalf("EncodedSize = %d, want 24", got)
	}
	if got := s.Rank(65535); got != 65536 {
		t.Fatalf("Rank = %d", got)
	}
	v, err := s.Select(65535)
	if err != nil || v != 65535 {
		t.Fatalf("Select(65535) = %d, %v", v, err)
	}
	// 清空后块消失。
	for v := uint32(0); v < 65536; v++ {
		s.Remove(v)
	}
	if len(s.blocks) != 0 || s.Len() != 0 {
		t.Fatalf("not empty: blocks=%d len=%d", len(s.blocks), s.Len())
	}
	if got := s.EncodedSize(); got != 8 {
		t.Fatalf("EncodedSize = %d, want 8", got)
	}
}

// 空块：块内元素删光后块被移除，编码中不出现空块。
func TestEmptyBlock(t *testing.T) {
	s := New()
	s.Add(7 << 16)
	s.Add(7<<16 | 1)
	s.Add(1 << 16)
	if len(s.blocks) != 2 {
		t.Fatalf("blocks = %d, want 2", len(s.blocks))
	}
	s.Remove(7 << 16)
	s.Remove(7<<16 | 1)
	if len(s.blocks) != 1 || s.blocks[0].key != 1 {
		t.Fatalf("blocks = %v", s.blocks)
	}
	d, err := Decode(s.Encode())
	if err != nil {
		t.Fatal(err)
	}
	mustCollect(t, d, []uint32{1 << 16})
}

// run 拆分与合并。
func TestRunSplitMerge(t *testing.T) {
	s := New()
	for v := uint32(100); v <= 200; v++ {
		s.Add(v)
	}
	rc, ok := s.blocks[0].c.(*runsC)
	if !ok {
		t.Fatalf("kind = %d, want runs", s.blocks[0].c.containerKind())
	}
	if !reflect.DeepEqual(rc.runs, []run{{100, 200}}) {
		t.Fatalf("runs = %v", rc.runs)
	}
	// 中间删除 → 拆成两段。
	s.Remove(150)
	rc = s.blocks[0].c.(*runsC)
	if !reflect.DeepEqual(rc.runs, []run{{100, 149}, {151, 200}}) {
		t.Fatalf("after split runs = %v", rc.runs)
	}
	// 补回桥接值 → 合并为一段。
	s.Add(150)
	rc = s.blocks[0].c.(*runsC)
	if !reflect.DeepEqual(rc.runs, []run{{100, 200}}) {
		t.Fatalf("after merge runs = %v", rc.runs)
	}
	// 端点删除 → 收缩。
	s.Remove(100)
	s.Remove(200)
	rc = s.blocks[0].c.(*runsC)
	if !reflect.DeepEqual(rc.runs, []run{{101, 199}}) {
		t.Fatalf("after shrink runs = %v", rc.runs)
	}
	// 相邻扩展。
	s.Add(100)
	s.Add(200)
	rc = s.blocks[0].c.(*runsC)
	if !reflect.DeepEqual(rc.runs, []run{{100, 200}}) {
		t.Fatalf("after extend runs = %v", rc.runs)
	}
}

// 快照后修改不污染旧集合。
func TestSnapshotIsolation(t *testing.T) {
	s := New()
	rng := rand.New(rand.NewSource(7))
	for i := 0; i < 3000; i++ {
		s.Add(rng.Uint32())
	}
	for v := uint32(500); v < 700; v++ {
		s.Add(v)
	}
	snap := s.Snapshot()
	wantSnap := snap.Collect()

	// 大幅修改原集合：填满块 0、删块 1、随机增删。
	for v := uint32(0); v < 65536; v++ {
		s.Add(v)
	}
	for v := uint32(1 << 16); v < 2<<16; v++ {
		s.Remove(v)
	}
	for i := 0; i < 2000; i++ {
		x := rng.Uint32()
		if i%2 == 0 {
			s.Add(x)
		} else {
			s.Remove(x)
		}
	}
	if got := snap.Collect(); !reflect.DeepEqual(got, wantSnap) {
		t.Fatal("snapshot polluted by mutations of original")
	}

	// 反向：修改快照不影响原集合。
	wantOrig := s.Collect()
	for v := uint32(0); v < 65536; v++ {
		snap.Remove(v)
	}
	snap.Add(123456789)
	if got := s.Collect(); !reflect.DeepEqual(got, wantOrig) {
		t.Fatal("original polluted by mutations of snapshot")
	}
	if !snap.Contains(123456789) || s.Contains(123456789) {
		t.Fatal("snapshot/original divergence wrong")
	}
}

// Select 非法 k 报错。
func TestSelectErrors(t *testing.T) {
	s := New()
	if _, err := s.Select(0); err == nil {
		t.Fatal("empty Select(0) should fail")
	}
	s.Add(10)
	s.Add(20)
	if _, err := s.Select(-1); err == nil {
		t.Fatal("Select(-1) should fail")
	}
	if _, err := s.Select(2); err == nil {
		t.Fatal("Select(2) should fail")
	}
	if v, err := s.Select(1); err != nil || v != 20 {
		t.Fatalf("Select(1) = %d, %v", v, err)
	}
}

// 随机操作对照参考实现，覆盖三种容器共存与相互转换。
func TestRandomVsRef(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	s := New()
	ref := newRef()
	denseBlock := uint32(rng.Intn(1 << 16))
	for i := 0; i < 30000; i++ {
		var x uint32
		switch rng.Intn(4) {
		case 0: // 全域随机
			x = rng.Uint32()
		case 1: // 某一块内密集（触发 bitmap）
			x = denseBlock<<16 | uint32(rng.Intn(65536))
		case 2: // 连续段（触发 runs）
			base := rng.Uint32() & 0xffffff00
			for d := uint32(0); d < 16; d++ {
				s.Add(base + d)
				ref.add(base + d)
			}
			continue
		default: // 小范围聚簇
			x = uint32(rng.Intn(1 << 20))
		}
		if rng.Intn(100) < 65 {
			s.Add(x)
			ref.add(x)
		} else {
			s.Remove(x)
			ref.remove(x)
		}
	}
	if s.Len() != len(ref.m) {
		t.Fatalf("Len = %d, want %d", s.Len(), len(ref.m))
	}
	mustCollect(t, s, ref.sorted())
	for i := 0; i < 500; i++ {
		x := rng.Uint32()
		if i%3 == 0 {
			x = denseBlock<<16 | uint32(rng.Intn(65536))
		}
		if got, want := s.Rank(x), ref.rank(x); got != want {
			t.Fatalf("Rank(%d) = %d, want %d", x, got, want)
		}
	}
	n := s.Len()
	ks := []int{0, 1, n / 3, n / 2, n - 1}
	for i := 0; i < 100; i++ {
		ks = append(ks, rng.Intn(n))
	}
	for _, k := range ks {
		got, err := s.Select(k)
		if err != nil {
			t.Fatalf("Select(%d): %v", k, err)
		}
		want, _ := ref.sel(k)
		if got != want {
			t.Fatalf("Select(%d) = %d, want %d", k, got, want)
		}
	}
	if _, err := s.Select(n); err == nil {
		t.Fatal("Select(Len) should fail")
	}
	// 编码往返一致。
	d, err := Decode(s.Encode())
	if err != nil {
		t.Fatal(err)
	}
	mustCollect(t, d, ref.sorted())
}
