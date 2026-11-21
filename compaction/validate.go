package compaction

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
)

// ErrInvalidManifest 表示输入清单结构或候选合法性不满足要求。
var ErrInvalidManifest = errors.New("compaction: invalid manifest")

// Validate 对清单做完整结构校验：
//   - 记录种类、区间半开约束、序号全局唯一；
//   - 保留区间与最新快照合法、页/字典/候选 ID 唯一且无悬空引用；
//   - 候选页只能原样复制被替换页的点记录，或把其中的区间墓碑截短为
//     同序号、覆盖范围不超出原区间的碎片（同序号碎片允许重叠）；
//   - 候选页不得创造新键值、新序号或新记录种类；
//   - 候选页数不超过 22。
func (m *Manifest) Validate() error {
	for i, iv := range m.RetainIntervals {
		if iv.Lo > iv.Hi {
			return fmt.Errorf("%w: retain interval %d inverted: %d > %d", ErrInvalidManifest, i, iv.Lo, iv.Hi)
		}
	}

	seenSeq := map[uint64]struct{}{}
	seenPageID := map[uint32]struct{}{}
	for _, p := range m.ReplacedPages {
		if _, dup := seenPageID[p.ID]; dup {
			return fmt.Errorf("%w: duplicated replaced page id %d", ErrInvalidManifest, p.ID)
		}
		seenPageID[p.ID] = struct{}{}
		for _, r := range p.Records {
			if err := validateRecord(r, seenSeq, "replaced page "+itoa32(p.ID)); err != nil {
				return err
			}
		}
	}
	for _, r := range m.OtherLayers {
		if err := validateRecord(r, seenSeq, "other layers"); err != nil {
			return err
		}
	}

	dictData := map[uint32][]byte{}
	seenDictID := map[uint32]struct{}{}
	for _, d := range m.Dictionaries {
		if _, dup := seenDictID[d.ID]; dup {
			return fmt.Errorf("%w: duplicated dictionary id %d", ErrInvalidManifest, d.ID)
		}
		seenDictID[d.ID] = struct{}{}
		dictData[d.ID] = d.Data
	}

	if len(m.Candidates) > maxCandidates {
		return fmt.Errorf("%w: %d candidates exceed limit %d", ErrInvalidManifest, len(m.Candidates), maxCandidates)
	}
	seenCandID := map[uint32]struct{}{}
	for _, c := range m.Candidates {
		if _, dup := seenCandID[c.ID]; dup {
			return fmt.Errorf("%w: duplicated candidate id %d", ErrInvalidManifest, c.ID)
		}
		seenCandID[c.ID] = struct{}{}
		if len(c.Encoding) == 0 {
			return fmt.Errorf("%w: candidate %d has empty encoding", ErrInvalidManifest, c.ID)
		}
		refSeen := map[uint32]struct{}{}
		for _, did := range c.DictIDs {
			if _, ok := dictData[did]; !ok {
				return fmt.Errorf("%w: candidate %d references unknown dictionary %d", ErrInvalidManifest, c.ID, did)
			}
			if _, dup := refSeen[did]; dup {
				return fmt.Errorf("%w: candidate %d references dictionary %d more than once", ErrInvalidManifest, c.ID, did)
			}
			refSeen[did] = struct{}{}
		}
		for _, r := range c.Records {
			if err := m.validateCandidateRecord(r, c.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateRecord(r Record, seenSeq map[uint64]struct{}, where string) error {
	switch r.Kind {
	case KindPointValue:
		if len(r.Key) == 0 {
			return fmt.Errorf("%w: %s seq %d: empty point key", ErrInvalidManifest, where, r.Seq)
		}
		// nil 值非法；空字节串是合法的非缺失值，二者必须区分。
		if r.Value == nil {
			return fmt.Errorf("%w: %s seq %d: nil point value", ErrInvalidManifest, where, r.Seq)
		}
	case KindPointDelete:
		if len(r.Key) == 0 {
			return fmt.Errorf("%w: %s seq %d: empty point key", ErrInvalidManifest, where, r.Seq)
		}
	case KindRangeDelete:
		if bytes.Compare(r.Key, r.End) >= 0 {
			return fmt.Errorf("%w: %s seq %d: range tombstone not half-open [start,end)", ErrInvalidManifest, where, r.Seq)
		}
	default:
		return fmt.Errorf("%w: %s seq %d: unknown record kind %d", ErrInvalidManifest, where, r.Seq, r.Kind)
	}
	if _, dup := seenSeq[r.Seq]; dup {
		return fmt.Errorf("%w: %s: duplicated sequence number %d", ErrInvalidManifest, where, r.Seq)
	}
	seenSeq[r.Seq] = struct{}{}
	return nil
}

// validateCandidateRecord 强制候选记录只能复制旧点记录或截短旧区间墓碑。
func (m *Manifest) validateCandidateRecord(r Record, candID uint32) error {
	if r.Kind != KindPointValue && r.Kind != KindPointDelete && r.Kind != KindRangeDelete {
		return fmt.Errorf("%w: candidate %d seq %d: unknown record kind %d", ErrInvalidManifest, candID, r.Seq, r.Kind)
	}
	// 序号必须来自某个被替换页，且同序号只有一条原始记录（序号全局唯一）。
	var origin *Record
	for pi := range m.ReplacedPages {
		for ri := range m.ReplacedPages[pi].Records {
			if m.ReplacedPages[pi].Records[ri].Seq == r.Seq {
				origin = &m.ReplacedPages[pi].Records[ri]
				break
			}
		}
		if origin != nil {
			break
		}
	}
	if origin == nil {
		return fmt.Errorf("%w: candidate %d seq %d: sequence not found in replaced pages", ErrInvalidManifest, candID, r.Seq)
	}
	if r.Kind != origin.Kind {
		return fmt.Errorf("%w: candidate %d seq %d: kind differs from original record", ErrInvalidManifest, candID, r.Seq)
	}
	switch origin.Kind {
	case KindPointValue:
		if !bytes.Equal(r.Key, origin.Key) || !bytes.Equal(r.Value, origin.Value) {
			return fmt.Errorf("%w: candidate %d seq %d: point value not copied verbatim", ErrInvalidManifest, candID, r.Seq)
		}
	case KindPointDelete:
		if !bytes.Equal(r.Key, origin.Key) {
			return fmt.Errorf("%w: candidate %d seq %d: point delete not copied verbatim", ErrInvalidManifest, candID, r.Seq)
		}
	case KindRangeDelete:
		// 同序号区间碎片：键空间必须包含于原半开区间内，下界可右移、
		// 上界可左移；不同碎片之间允许重叠。
		if bytes.Compare(r.Key, origin.Key) < 0 ||
			bytes.Compare(r.End, origin.End) > 0 ||
			bytes.Compare(r.Key, r.End) >= 0 {
			return fmt.Errorf("%w: candidate %d seq %d: fragment extends beyond original range tombstone", ErrInvalidManifest, candID, r.Seq)
		}
	}
	return nil
}

func itoa32(u uint32) string {
	if u == 0 {
		return "0"
	}
	var buf [12]byte
	i := len(buf)
	for u > 0 {
		i--
		buf[i] = byte('0' + u%10)
		u /= 10
	}
	return string(buf[i:])
}

// sortedCandidates 返回按页编号升序排列的候选页副本视图，保证枚举与
// 页编号向量比较具有确定性。
func (m *Manifest) sortedCandidates() []CandidatePage {
	out := make([]CandidatePage, len(m.Candidates))
	copy(out, m.Candidates)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
