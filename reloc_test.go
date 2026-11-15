package netsec

import (
	"encoding/binary"
	"errors"
	"math"
	"testing"
)

func textSeg(base uint64, size int) Segment {
	return Segment{Base: base, Data: make([]byte, size), Perm: PermRead | PermExec}
}

func dataSeg(base uint64, size int) Segment {
	return Segment{Base: base, Data: make([]byte, size), Perm: PermRead | PermWrite}
}

func requireErr(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("got error %v, want %v", err, want)
	}
}

// 合法路径：8 字节写 + 合法负加数，成功后宿主封只读。
func TestApplyValidNegativeAddend(t *testing.T) {
	segs := []Segment{
		dataSeg(0x400000, 16), // 目标
		textSeg(0x500000, 64), // 符号
	}
	relocs := []Reloc{{
		TargetSeg: 0, Offset: 8, Width: 8,
		SymbolSeg: 1, SymOffset: 0x20, Addend: -0x10,
	}}
	if err := ApplyRelocations(segs, relocs); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := uint64(0x500000 + 0x20 - 0x10)
	if got := binary.LittleEndian.Uint64(segs[0].Data[8:16]); got != want {
		t.Fatalf("got %#x, want %#x", got, want)
	}
	if got := binary.LittleEndian.Uint64(segs[0].Data[0:8]); got != 0 {
		t.Fatalf("untouched bytes modified: %#x", got)
	}

	SealReadOnly(&segs[0])
	if segs[0].Perm&PermWrite != 0 {
		t.Fatal("segment still writable after seal")
	}
	// 封只读后再次重定位必须被拒绝。
	err := ApplyRelocations(segs, relocs)
	requireErr(t, err, ErrTargetNotW)
}

// 截断写：宽度 4 装不下符号地址，必须拒绝而非截断。
func TestTruncatedWriteRejected(t *testing.T) {
	segs := []Segment{
		dataSeg(0x10000, 16),
		textSeg(0x1_0000_0000, 16), // 符号地址超过 32 位
	}
	relocs := []Reloc{{
		TargetSeg: 0, Offset: 0, Width: 4,
		SymbolSeg: 1, SymOffset: 0, Addend: 0,
	}}
	requireErr(t, ApplyRelocations(segs, relocs), ErrValueTruncated)
	for i, b := range segs[0].Data {
		if b != 0 {
			t.Fatalf("byte %d modified on failure: %#x", i, b)
		}
	}
}

// 地址回绕：符号地址+加数溢出 uint64，或负加数使结果为负。
func TestAddressWraparound(t *testing.T) {
	cases := []struct {
		name   string
		base   uint64
		addend int64
	}{
		{"positive overflow", math.MaxUint64 - 8, 0x100},
		{"negative below zero", 0x1000, -0x2000},
		{"min int64 addend", 0x1000, math.MinInt64},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			segs := []Segment{
				dataSeg(0x8000_0000, 16),
				textSeg(tc.base, 8),
			}
			relocs := []Reloc{{
				TargetSeg: 0, Offset: 0, Width: 8,
				SymbolSeg: 1, SymOffset: 0, Addend: tc.addend,
			}}
			requireErr(t, ApplyRelocations(segs, relocs), ErrValueOverflow)
		})
	}
}

// 段区间自身回绕地址空间也必须拒绝。
func TestSegmentIntervalWraps(t *testing.T) {
	segs := []Segment{
		{Base: math.MaxUint64 - 3, Data: make([]byte, 8), Perm: PermRead | PermWrite},
	}
	requireErr(t, ApplyRelocations(segs, nil), ErrSegOverflow)
}

// 相交与重复的重定位项必须拒绝。
func TestOverlappingRelocations(t *testing.T) {
	mk := func(off1, off2 uint64, w1, w2 uint8) ([]Segment, []Reloc) {
		segs := []Segment{dataSeg(0x400000, 32), textSeg(0x500000, 16)}
		relocs := []Reloc{
			{TargetSeg: 0, Offset: off1, Width: w1, SymbolSeg: 1, SymOffset: 0},
			{TargetSeg: 0, Offset: off2, Width: w2, SymbolSeg: 1, SymOffset: 4},
		}
		return segs, relocs
	}
	t.Run("duplicate", func(t *testing.T) {
		segs, relocs := mk(8, 8, 8, 8)
		requireErr(t, ApplyRelocations(segs, relocs), ErrRelocOverlap)
	})
	t.Run("partial intersection", func(t *testing.T) {
		segs, relocs := mk(8, 12, 8, 8) // [8,16) 与 [12,20) 相交
		requireErr(t, ApplyRelocations(segs, relocs), ErrRelocOverlap)
	})
	t.Run("adjacent ok", func(t *testing.T) {
		segs, relocs := mk(8, 16, 8, 8) // [8,16) 与 [16,24) 相邻不相交
		if err := ApplyRelocations(segs, relocs); err != nil {
			t.Fatalf("adjacent relocations rejected: %v", err)
		}
	})
}

// W+X 目标段必须拒绝；纯可写段允许。
func TestWritableExecutableTargetRejected(t *testing.T) {
	segs := []Segment{
		{Base: 0x400000, Data: make([]byte, 16), Perm: PermRead | PermWrite | PermExec},
		textSeg(0x500000, 16),
	}
	relocs := []Reloc{{
		TargetSeg: 0, Offset: 0, Width: 8,
		SymbolSeg: 1, SymOffset: 0, Addend: 0,
	}}
	requireErr(t, ApplyRelocations(segs, relocs), ErrTargetExec)
	for i, b := range segs[0].Data {
		if b != 0 {
			t.Fatalf("byte %d modified on failure: %#x", i, b)
		}
	}
}

// 目标越界（全宽不落在段内）必须拒绝。
func TestTargetOutOfRange(t *testing.T) {
	segs := []Segment{dataSeg(0x400000, 16), textSeg(0x500000, 16)}
	relocs := []Reloc{{
		TargetSeg: 0, Offset: 12, Width: 8, // [12,20) 超出 16 字节段
		SymbolSeg: 1, SymOffset: 0,
	}}
	requireErr(t, ApplyRelocations(segs, relocs), ErrTargetRange)
}

// 符号落在不可读段必须拒绝。
func TestSymbolNotReadable(t *testing.T) {
	segs := []Segment{
		dataSeg(0x400000, 16),
		{Base: 0x500000, Data: make([]byte, 16), Perm: PermWrite}, // 不可读
	}
	relocs := []Reloc{{
		TargetSeg: 0, Offset: 0, Width: 8,
		SymbolSeg: 1, SymOffset: 0,
	}}
	requireErr(t, ApplyRelocations(segs, relocs), ErrSymbolNotR)
}

// 段区间相交必须拒绝。
func TestOverlappingSegments(t *testing.T) {
	segs := []Segment{
		dataSeg(0x400000, 0x2000),
		textSeg(0x401000, 0x2000),
	}
	requireErr(t, ApplyRelocations(segs, nil), ErrSegOverlap)
}

// 原子性：多条重定位中最后一条非法，前面合法的也不得写入。
func TestAtomicityOnFailure(t *testing.T) {
	segs := []Segment{dataSeg(0x400000, 32), textSeg(0x500000, 16)}
	relocs := []Reloc{
		{TargetSeg: 0, Offset: 0, Width: 8, SymbolSeg: 1, SymOffset: 0}, // 合法
		{TargetSeg: 0, Offset: 8, Width: 3, SymbolSeg: 1, SymOffset: 0}, // 非法宽度
	}
	requireErr(t, ApplyRelocations(segs, relocs), ErrBadWidth)
	for i, b := range segs[0].Data {
		if b != 0 {
			t.Fatalf("byte %d modified on failure: %#x", i, b)
		}
	}
}
