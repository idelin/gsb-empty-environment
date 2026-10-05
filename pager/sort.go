package pager

import (
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"strings"
)

// NullOrder 控制空值在排序中的位置。
type NullOrder int

const (
	// NullDefault 遵循 SQL 惯例：升序时空值排最后，降序时空值排最前。
	NullDefault NullOrder = iota
	// NullsFirst 空值排最前（与方向无关）。
	NullsFirst
	// NullsLast 空值排最后（与方向无关）。
	NullsLast
)

// SortField 描述一个排序字段。
type SortField struct {
	Name  string
	Desc  bool
	Nulls NullOrder
}

// Asc 构造升序字段（空值默认排最后）。
func Asc(name string) SortField { return SortField{Name: name} }

// Desc 构造降序字段（空值默认排最前）。
func Desc(name string) SortField { return SortField{Name: name, Desc: true} }

// WithNulls 返回设置空值规则后的副本。
func (f SortField) WithNulls(o NullOrder) SortField { f.Nulls = o; return f }

func (f SortField) nullsFirst() bool {
	switch f.Nulls {
	case NullsFirst:
		return true
	case NullsLast:
		return false
	default:
		return f.Desc
	}
}

func (f SortField) canonical() string {
	dir := "asc"
	if f.Desc {
		dir = "desc"
	}
	return fmt.Sprintf("%s|%s|%d", f.Name, dir, f.Nulls)
}

// SortSpec 是一组排序字段；行先按各字段比较，最后以唯一编号升序决胜。
type SortSpec []SortField

// canonical 返回排序规则的规范字符串表示。
func (s SortSpec) canonical() string {
	parts := make([]string, len(s))
	for i, f := range s {
		parts[i] = f.canonical()
	}
	return strings.Join(parts, ";")
}

// Hash 返回排序规则的指纹，用于游标与排序规则的绑定校验。
func (s SortSpec) Hash() string {
	h := fnv.New64a()
	h.Write([]byte(s.canonical()))
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], h.Sum64())
	return fmt.Sprintf("%x", buf[:])
}

// compareKeys 按排序规则比较两行排序键（不含唯一编号决胜）。
func (s SortSpec) compareKeys(a, b []Value) int {
	for i, f := range s {
		av, bv := a[i], b[i]
		// 空值位置只由 NullOrder 决定，不随方向反转（与 SQL 一致）。
		if av.Null || bv.Null {
			switch {
			case av.Null && bv.Null:
				continue
			case av.Null:
				if f.nullsFirst() {
					return -1
				}
				return 1
			default:
				if f.nullsFirst() {
					return 1
				}
				return -1
			}
		}
		c := compareNonNull(av, bv)
		if c == 0 {
			continue
		}
		if f.Desc {
			return -c
		}
		return c
	}
	return 0
}
