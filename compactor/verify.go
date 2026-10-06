package compactor

import "fmt"

// NaiveQuery 是朴素历史查询：扫描全部段，返回序号 <= seq 的最新条目
// 对该键的可见值。最新条目为 Put 时返回 (value, true)；为 Delete
// 或不存在时返回 ("", false)。它不做任何索引，是压实的参照语义。
func NaiveQuery(segments []Segment, key string, seq uint64) (string, bool) {
	best := int(-1)
	var bestEntry Entry
	for _, seg := range segments {
		for _, e := range seg.Entries {
			if e.Key != key || e.Seq > seq {
				continue
			}
			if best < 0 || e.Seq > bestEntry.Seq {
				bestEntry = e
				best = 1
			}
		}
	}
	if best < 0 || bestEntry.Kind == Delete {
		return "", false
	}
	return bestEntry.Value, true
}

// Mismatch 描述一次压实前后查询结果不一致。
type Mismatch struct {
	Key         string
	Seq         uint64
	BeforeVal   string
	BeforeFound bool
	AfterVal    string
	AfterFound  bool
}

func (m Mismatch) String() string {
	return fmt.Sprintf("key=%q seq=%d: 压实前=(%q,%v) 压实后=(%q,%v)",
		m.Key, m.Seq, m.BeforeVal, m.BeforeFound, m.AfterVal, m.AfterFound)
}

// VerifyEquivalence 用朴素历史查询验证压实等价性：
// 对 [oldestSnapshot, maxSeq] 内每个序号、每个键，分别在两组段上
// 执行 NaiveQuery，结果必须完全一致。序号小于 oldestSnapshot 的
// 历史允许发散——那正是压实回收的空间。
func VerifyEquivalence(before, after []Segment, keys []string, oldestSnapshot, maxSeq uint64) []Mismatch {
	var mismatches []Mismatch
	for _, key := range keys {
		for seq := oldestSnapshot; seq <= maxSeq; seq++ {
			bv, bf := NaiveQuery(before, key, seq)
			av, af := NaiveQuery(after, key, seq)
			if bv != av || bf != af {
				mismatches = append(mismatches, Mismatch{key, seq, bv, bf, av, af})
			}
		}
	}
	return mismatches
}

// MaxSeq 返回所有段中出现的最大序号。
func MaxSeq(segments []Segment) uint64 {
	var max uint64
	for _, seg := range segments {
		for _, e := range seg.Entries {
			if e.Seq > max {
				max = e.Seq
			}
		}
	}
	return max
}
