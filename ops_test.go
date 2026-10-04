package set32

import (
	"reflect"
	"testing"
)

// 构造三种容器形态的集合（同一块 0 内），并附加跨块内容。
func makeArraySet() *Set {
	s := New()
	for _, v := range []uint32{1, 100, 200, 5000, 65535} {
		s.Add(v)
	}
	s.Add(3<<16 | 7)
	s.Add(5 << 16)
	return s
}

func makeBitmapSet() *Set {
	s := New()
	for v := uint32(0); v < 65536; v += 2 { // 块 0 内全部偶数 → bitmap
		s.Add(v)
	}
	for v := uint32(3 << 16); v < 3<<16+1000; v += 2 {
		s.Add(v)
	}
	return s
}

func makeRunsSet() *Set {
	s := New()
	for v := uint32(10); v <= 5000; v++ {
		s.Add(v)
	}
	s.Add(6000)
	for v := uint32(5 << 16); v < 5<<16+500; v++ {
		s.Add(v)
	}
	return s
}

func checkKind(t *testing.T, s *Set, key uint16, want kind) {
	t.Helper()
	i, ok := s.find(key)
	if !ok {
		t.Fatalf("block %d missing", key)
	}
	if got := s.blocks[i].c.containerKind(); got != want {
		t.Fatalf("block %d kind = %d, want %d", key, got, want)
	}
}

func toRef(s *Set) *refSet {
	r := newRef()
	for _, v := range s.Collect() {
		r.add(v)
	}
	return r
}

// 三种容器两两组合（含同种）× AND/OR/XOR，对照参考实现。
func TestAllContainerCombos(t *testing.T) {
	makers := map[string]func() *Set{
		"array":  makeArraySet,
		"bitmap": makeBitmapSet,
		"runs":   makeRunsSet,
	}
	// 确认三种集合在块 0 确实落到了预期容器。
	checkKind(t, makeArraySet(), 0, kindArray)
	checkKind(t, makeBitmapSet(), 0, kindBitmap)
	checkKind(t, makeRunsSet(), 0, kindRuns)

	for an, am := range makers {
		for bn, bm := range makers {
			a, b := am(), bm()
			ra, rb := toRef(a), toRef(b)
			cases := []struct {
				name string
				got  *Set
				want *refSet
			}{
				{"AND", a.And(b), ra.and(rb)},
				{"OR", a.Or(b), ra.or(rb)},
				{"XOR", a.Xor(b), ra.xor(rb)},
			}
			for _, c := range cases {
				if got := c.got.Collect(); !reflect.DeepEqual(got, c.want.sorted()) {
					t.Fatalf("%s %s %s mismatch: got %d elems, want %d",
						an, c.name, bn, len(got), len(c.want.m))
				}
				// 结果编码必须能被严格解码器接受且往返一致。
				enc := c.got.Encode()
				d, err := Decode(enc)
				if err != nil {
					t.Fatalf("%s %s %s decode: %v", an, c.name, bn, err)
				}
				if !reflect.DeepEqual(d.Encode(), enc) {
					t.Fatalf("%s %s %s re-encode differs", an, c.name, bn)
				}
			}
		}
	}
}

// 集合运算不应展开 32 位全域：空集与满块集合运算的代价与块数成正比。
func TestOpsDoNotExpandDomain(t *testing.T) {
	full := New()
	for v := uint32(0); v < 65536; v++ {
		full.Add(v)
	}
	empty := New()
	if got := full.And(empty).Len(); got != 0 {
		t.Fatalf("AND empty = %d", got)
	}
	if got := full.Or(empty).Len(); got != 65536 {
		t.Fatalf("OR empty = %d", got)
	}
	if got := full.Xor(empty).Len(); got != 65536 {
		t.Fatalf("XOR empty = %d", got)
	}
	// 结果只有 1 个块，编码 24 字节，而非全域展开。
	if n := len(full.Or(empty).blocks); n != 1 {
		t.Fatalf("blocks = %d, want 1", n)
	}
	// 高块与低块做运算，中间不存在实体块。
	a := New()
	a.Add(5)
	b := New()
	b.Add(4294967295)
	u := a.Or(b)
	if len(u.blocks) != 2 {
		t.Fatalf("blocks = %d, want 2", len(u.blocks))
	}
	if got := u.Rank(4294967295); got != 2 {
		t.Fatalf("Rank = %d, want 2", got)
	}
}
