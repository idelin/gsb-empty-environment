// Package restore 从可能重叠、末尾被截断的日志段中恢复连续日志前缀。
//
// 每个段声明起止序号（闭区间）与内容的校验和；段实际携带的记录数
// 可能少于声明范围（末尾截断）。仅当不同段对同一序号给出的内容完全
// 一致时才合并；发现冲突时报告冲突的最早序号。只有从指定起点连续、
// 且来自校验通过段的记录才会被发布，缺口之后的内容不会被悄悄接上。
package restore

import (
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"sort"
)

// Segment 是一段日志：声明覆盖序号 [Start, End]（闭区间），
// Checksum 为 Records 实际携带内容的校验和。len(Records) 小于
// End-Start+1 时表示段末尾被截断。
type Segment struct {
	Start    uint64
	End      uint64
	Checksum uint64
	Records  [][]byte
}

// Record 是恢复输出中的一条日志记录。
type Record struct {
	Seq  uint64
	Data []byte
}

// RejectReason 描述段被拒绝的原因。
type RejectReason int

const (
	RejectBadRange    RejectReason = iota // Start > End，区间非法
	RejectTooMany                         // 携带记录数超过声明区间
	RejectBadChecksum                     // 校验和不匹配，内容不可信
)

func (r RejectReason) String() string {
	switch r {
	case RejectBadRange:
		return "bad range"
	case RejectTooMany:
		return "more records than declared range"
	case RejectBadChecksum:
		return "checksum mismatch"
	}
	return "unknown"
}

// Rejected 记录一个被拒绝（未参与合并）的段。
type Rejected struct {
	Index  int // 在输入切片中的下标
	Reason RejectReason
}

// ConflictError 表示不同段对同一序号给出了不一致的内容。
// Seq 为发生冲突的最早序号。
type ConflictError struct {
	Seq       uint64   // 最早冲突序号
	Conflicts []uint64 // 全部冲突序号，升序
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("restore: conflicting content, earliest at seq %d (%d total)", e.Seq, len(e.Conflicts))
}

// Result 是恢复结果。Records 与 Sources 等长且一一对应。
type Result struct {
	From     uint64     // 发布起点
	Records  []Record   // 从 From 开始的连续、校验通过的前缀
	Sources  []int      // 每条已发布记录来源段在输入中的下标
	Rejected []Rejected // 被拒绝的段
	Next     uint64     // 发布停止处的序号（From + len(Records)）
}

// Checksum 计算一组记录的校验和：对每条记录先写 8 字节大端长度，
// 再写内容，整体取 FNV-1a 64。
func Checksum(records [][]byte) uint64 {
	h := fnv.New64a()
	var buf [8]byte
	for _, rec := range records {
		binary.BigEndian.PutUint64(buf[:], uint64(len(rec)))
		h.Write(buf[:])
		h.Write(rec)
	}
	return h.Sum64()
}

// validate 检查段的结构合法性与校验和。
func validate(seg Segment) (RejectReason, bool) {
	if seg.Start > seg.End {
		return RejectBadRange, false
	}
	if uint64(len(seg.Records)) > seg.End-seg.Start+1 {
		return RejectTooMany, false
	}
	if Checksum(seg.Records) != seg.Checksum {
		return RejectBadChecksum, false
	}
	return 0, true
}

// Restore 合并 segs 并发布从 from 开始的连续前缀。
//
// 校验或结构不合法的段被跳过并记入 Result.Rejected。来自不同有效段、
// 序号相同但内容不同的记录导致返回 *ConflictError（含最早冲突序号），
// 此时不发布任何内容。缺口之后的记录即使存在也不会被发布。
func Restore(from uint64, segs []Segment) (*Result, error) {
	res := &Result{From: from, Next: from}

	type entry struct {
		data   []byte
		source int
	}
	merged := make(map[uint64]entry)
	conflictSet := make(map[uint64]struct{})

	for i, seg := range segs {
		if reason, ok := validate(seg); !ok {
			res.Rejected = append(res.Rejected, Rejected{Index: i, Reason: reason})
			continue
		}
		for j, rec := range seg.Records {
			seq := seg.Start + uint64(j)
			if prev, seen := merged[seq]; seen {
				if !bytesEqual(prev.data, rec) {
					conflictSet[seq] = struct{}{}
				}
				continue // 内容一致：去重，保留先出现段的来源
			}
			merged[seq] = entry{data: rec, source: i}
		}
	}

	if len(conflictSet) > 0 {
		conflicts := make([]uint64, 0, len(conflictSet))
		for seq := range conflictSet {
			conflicts = append(conflicts, seq)
		}
		sort.Slice(conflicts, func(a, b int) bool { return conflicts[a] < conflicts[b] })
		return nil, &ConflictError{Seq: conflicts[0], Conflicts: conflicts}
	}

	// 仅发布从 from 起连续的前缀；遇到缺口即停止。
	for seq := from; ; seq++ {
		e, ok := merged[seq]
		if !ok {
			res.Next = seq
			break
		}
		res.Records = append(res.Records, Record{Seq: seq, Data: e.data})
		res.Sources = append(res.Sources, e.source)
	}
	return res, nil
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
