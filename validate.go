package netsec

import (
	"errors"
	"fmt"
	"sort"
)

// MaxMapEntries 是内核 newuidmap/newgidmap 允许的单次映射条目上限。
const MaxMapEntries = 340

// IDMapEntry 描述一条 uid_map/gid_map 映射：容器内起点、宿主起点与长度。
type IDMapEntry struct {
	ContainerStart uint32
	HostStart      uint32
	Length         uint32
}

// SubIDRange 是 /etc/subuid、/etc/subgid 中分配给调用者的一段区间。
type SubIDRange struct {
	Start  uint32
	Length uint32
}

var (
	ErrEmptyMap        = errors.New("netsec: map has no entries")
	ErrTooManyEntries  = errors.New("netsec: map exceeds 340 entries")
	ErrZeroLength      = errors.New("netsec: map entry has zero length")
	ErrOverflow        = errors.New("netsec: start+length overflows uint32")
	ErrHostNotAllowed  = errors.New("netsec: host id range not permitted for caller")
	ErrOverlap         = errors.New("netsec: container id ranges overlap")
	ErrSetgroupsNeeded = errors.New("netsec: setgroups=deny must be written before gid_map without CAP_SETGID")
)

func overflows(start, length uint32) bool {
	return uint64(start)+uint64(length) > 1<<32
}

func rangeContains(r SubIDRange, start, length uint32) bool {
	return start >= r.Start &&
		uint64(start)+uint64(length) <= uint64(r.Start)+uint64(r.Length)
}

// hostAllowed 判定一条宿主段是否合法：要么是调用者自身 id 且长度为 1，
// 要么完整落在调用者被分配的某一段 subuid/subgid 区间内。
func hostAllowed(callerID uint32, subranges []SubIDRange, e IDMapEntry) bool {
	if e.HostStart == callerID && e.Length == 1 {
		return true
	}
	for _, r := range subranges {
		if rangeContains(r, e.HostStart, e.Length) {
			return true
		}
	}
	return false
}

// ValidateMap 在写 uid_map/gid_map 之前校验请求：
// 条目数不超过 340，长度非零，起点加长度按 uint32 不回绕，
// 宿主段合法，且容器段互不重叠。callerID 为调用者真实 uid/gid，
// subranges 为其在 subuid/subgid 中被分配的区间表。
func ValidateMap(callerID uint32, subranges []SubIDRange, entries []IDMapEntry) error {
	if len(entries) == 0 {
		return ErrEmptyMap
	}
	if len(entries) > MaxMapEntries {
		return ErrTooManyEntries
	}
	for _, e := range entries {
		if e.Length == 0 {
			return ErrZeroLength
		}
		if overflows(e.ContainerStart, e.Length) || overflows(e.HostStart, e.Length) {
			return fmt.Errorf("%w: start %d length %d", ErrOverflow, e.ContainerStart, e.Length)
		}
		if !hostAllowed(callerID, subranges, e) {
			return fmt.Errorf("%w: host start %d length %d", ErrHostNotAllowed, e.HostStart, e.Length)
		}
	}

	sorted := make([]IDMapEntry, len(entries))
	copy(sorted, entries)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].ContainerStart < sorted[j].ContainerStart
	})
	for i := 1; i < len(sorted); i++ {
		prevEnd := uint64(sorted[i-1].ContainerStart) + uint64(sorted[i-1].Length)
		if uint64(sorted[i].ContainerStart) < prevEnd {
			return fmt.Errorf("%w: container start %d", ErrOverlap, sorted[i].ContainerStart)
		}
	}
	return nil
}

// MapWriter 按特权辅助程序的顺序约束写 setgroups、uid_map 与 gid_map。
type MapWriter struct {
	callerUID    uint32
	callerGID    uint32
	subuid       []SubIDRange
	subgid       []SubIDRange
	hasCapSetGID bool

	setgroupsDenied bool
	uidMap          []IDMapEntry
	gidMap          []IDMapEntry
}

// NewMapWriter 创建校验写入器；hasCapSetGID 表示调用者是否已持有 CAP_SETGID。
func NewMapWriter(callerUID, callerGID uint32, subuid, subgid []SubIDRange, hasCapSetGID bool) *MapWriter {
	return &MapWriter{
		callerUID:    callerUID,
		callerGID:    callerGID,
		subuid:       subuid,
		subgid:       subgid,
		hasCapSetGID: hasCapSetGID,
	}
}

// WriteSetgroups 记录 setgroups 文件内容；deny 为 true 表示写入 "deny"。
func (w *MapWriter) WriteSetgroups(deny bool) error {
	w.setgroupsDenied = deny
	return nil
}

// WriteUIDMap 校验并记录 uid_map。
func (w *MapWriter) WriteUIDMap(entries []IDMapEntry) error {
	if err := ValidateMap(w.callerUID, w.subuid, entries); err != nil {
		return err
	}
	w.uidMap = entries
	return nil
}

// WriteGIDMap 校验并记录 gid_map。调用者没有 CAP_SETGID 时，
// 必须先通过 WriteSetgroups(true) 写入 setgroups=deny。
func (w *MapWriter) WriteGIDMap(entries []IDMapEntry) error {
	if !w.hasCapSetGID && !w.setgroupsDenied {
		return ErrSetgroupsNeeded
	}
	if err := ValidateMap(w.callerGID, w.subgid, entries); err != nil {
		return err
	}
	w.gidMap = entries
	return nil
}

// UIDMap 返回已接受的 uid_map。
func (w *MapWriter) UIDMap() []IDMapEntry { return w.uidMap }

// GIDMap 返回已接受的 gid_map。
func (w *MapWriter) GIDMap() []IDMapEntry { return w.gidMap }
