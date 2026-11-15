package netsec

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sort"
)

// Perm 描述段页面的访问权限位。
type Perm uint8

const (
	PermRead Perm = 1 << iota
	PermWrite
	PermExec
)

// Segment 是一个待装载段：基址、字节内容与权限。
type Segment struct {
	Base uint64
	Data []byte
	Perm Perm
}

// Reloc 是一条重定位项：把 符号地址+加数 写入目标段内 Offset 处，
// 宽度为 4 或 8 字节，小端写入。
type Reloc struct {
	TargetSeg int
	Offset    uint64
	Width     uint8
	SymbolSeg int
	SymOffset uint64
	Addend    int64
}

var (
	ErrSegOverlap     = errors.New("segment intervals overlap")
	ErrSegOverflow    = errors.New("segment interval wraps address space")
	ErrBadWidth       = errors.New("relocation width must be 4 or 8")
	ErrBadSegRef      = errors.New("segment id out of range")
	ErrTargetRange    = errors.New("target range outside segment")
	ErrTargetNotW     = errors.New("target segment not writable")
	ErrTargetExec     = errors.New("target segment is executable")
	ErrSymbolRange    = errors.New("symbol offset outside segment")
	ErrSymbolNotR     = errors.New("symbol segment not readable")
	ErrValueOverflow  = errors.New("symbol address plus addend not representable")
	ErrValueTruncated = errors.New("value does not fit relocation width")
	ErrRelocOverlap   = errors.New("duplicate or intersecting relocations")
)

// end 返回段区间的右端点；区间不溢出地址空间时 ok 为 true。
func (s *Segment) end() (uint64, bool) {
	n := uint64(len(s.Data))
	if n > math.MaxUint64-s.Base {
		return 0, false
	}
	return s.Base + n, true
}

// validatedReloc 是验证后待写入的解析结果。
type validatedReloc struct {
	seg    int
	offset uint64
	width  uint8
	value  uint64
}

// validateSegments 校验段区间不溢出且不两两相交。
func validateSegments(segs []Segment) error {
	type interval struct{ lo, hi uint64 }
	ivs := make([]interval, 0, len(segs))
	for i := range segs {
		hi, ok := segs[i].end()
		if !ok {
			return fmt.Errorf("segment %d: %w", i, ErrSegOverflow)
		}
		if hi > segs[i].Base {
			ivs = append(ivs, interval{segs[i].Base, hi})
		}
	}
	sort.Slice(ivs, func(a, b int) bool { return ivs[a].lo < ivs[b].lo })
	for i := 1; i < len(ivs); i++ {
		if ivs[i].lo < ivs[i-1].hi {
			return ErrSegOverlap
		}
	}
	return nil
}

// addAddend 计算 addr+addend，结果须为可表示的 uint64。
func addAddend(addr uint64, addend int64) (uint64, error) {
	if addend >= 0 {
		if uint64(addend) > math.MaxUint64-addr {
			return 0, ErrValueOverflow
		}
		return addr + uint64(addend), nil
	}
	mag := uint64(-(addend + 1)) + 1 // |addend|，对 MinInt64 亦安全
	if mag > addr {
		return 0, ErrValueOverflow
	}
	return addr - mag, nil
}

// validateReloc 校验单条重定位并解析出待写值。
func validateReloc(segs []Segment, r *Reloc) (validatedReloc, error) {
	if r.Width != 4 && r.Width != 8 {
		return validatedReloc{}, ErrBadWidth
	}
	if r.TargetSeg < 0 || r.TargetSeg >= len(segs) ||
		r.SymbolSeg < 0 || r.SymbolSeg >= len(segs) {
		return validatedReloc{}, ErrBadSegRef
	}

	tgt := &segs[r.TargetSeg]
	if tgt.Perm&PermExec != 0 {
		return validatedReloc{}, ErrTargetExec
	}
	if tgt.Perm&PermWrite == 0 {
		return validatedReloc{}, ErrTargetNotW
	}
	tn := uint64(len(tgt.Data))
	w := uint64(r.Width)
	if r.Offset > tn || w > tn-r.Offset {
		return validatedReloc{}, ErrTargetRange
	}

	sym := &segs[r.SymbolSeg]
	if sym.Perm&PermRead == 0 {
		return validatedReloc{}, ErrSymbolNotR
	}
	if r.SymOffset >= uint64(len(sym.Data)) {
		return validatedReloc{}, ErrSymbolRange
	}
	// 段区间已验证不溢出，故 sym.Base+SymOffset 不会回绕。
	symAddr := sym.Base + r.SymOffset

	val, err := addAddend(symAddr, r.Addend)
	if err != nil {
		return validatedReloc{}, err
	}
	if r.Width == 4 && val > math.MaxUint32 {
		return validatedReloc{}, ErrValueTruncated
	}
	return validatedReloc{seg: r.TargetSeg, offset: r.Offset, width: r.Width, value: val}, nil
}

// checkRelocOverlap 拒绝目标字节区间重复或相交的重定位项。
func checkRelocOverlap(vs []validatedReloc) error {
	type span struct {
		seg    int
		lo, hi uint64
	}
	spans := make([]span, len(vs))
	for i, v := range vs {
		spans[i] = span{v.seg, v.offset, v.offset + uint64(v.width)}
	}
	sort.Slice(spans, func(a, b int) bool {
		if spans[a].seg != spans[b].seg {
			return spans[a].seg < spans[b].seg
		}
		return spans[a].lo < spans[b].lo
	})
	for i := 1; i < len(spans); i++ {
		if spans[i].seg == spans[i-1].seg && spans[i].lo < spans[i-1].hi {
			return ErrRelocOverlap
		}
	}
	return nil
}

// ApplyRelocations 先整体验证段表与重定位表，全部通过后原子写入；
// 任一校验失败则返回错误且不修改任何段内容。
func ApplyRelocations(segs []Segment, relocs []Reloc) error {
	if err := validateSegments(segs); err != nil {
		return err
	}
	vs := make([]validatedReloc, len(relocs))
	for i := range relocs {
		v, err := validateReloc(segs, &relocs[i])
		if err != nil {
			return fmt.Errorf("relocation %d: %w", i, err)
		}
		vs[i] = v
	}
	if err := checkRelocOverlap(vs); err != nil {
		return err
	}

	// 验证全部完成，写入不会失败。
	for _, v := range vs {
		dst := segs[v.seg].Data[v.offset : v.offset+uint64(v.width)]
		if v.width == 4 {
			binary.LittleEndian.PutUint32(dst, uint32(v.value))
		} else {
			binary.LittleEndian.PutUint64(dst, v.value)
		}
	}
	return nil
}

// SealReadOnly 供宿主在装载成功后调用，清除目标段的写权限。
func SealReadOnly(s *Segment) {
	s.Perm &^= PermWrite
}
