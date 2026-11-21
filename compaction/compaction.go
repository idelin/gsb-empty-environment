// Package compaction 为压缩线程生成并安装替换页。
//
// 记录模型：每条记录带全局唯一序号，分为点值、点删除、字节串半开区间
// [Start, End) 删除三类。快照读只考虑序号不晚于快照且覆盖该键的记录，
// 其中最大序号决定结果。读结果区分三种状态：
//   - 缺失（没有任何覆盖记录）；
//   - 空值/墓碑（点删除或区间删除，语义为 null）；
//   - 点值（值可以是空字节串，空值与缺失不同）。
package compaction

import (
	"bytes"
	"sort"
)

// Kind 标识记录种类。零值保留给“读缺失”。
type Kind uint8

const (
	// KindMissing 仅用于 ReadResult，表示没有任何覆盖记录。
	KindMissing Kind = 0
	// KindPointValue 点值记录，Value 为 nil 与为空切片是两种不同的值。
	KindPointValue Kind = 1
	// KindPointDelete 点删除记录，读结果为 null。
	KindPointDelete Kind = 2
	// KindRangeDelete 半开区间 [Key, End) 删除，读结果为 null。
	KindRangeDelete Kind = 3
)

// Record 是一条带唯一序号的旧记录。
//
// 点记录（KindPointValue/KindPointDelete）使用 Key；区间记录使用
// Key 作为下界、End 作为开区间上界，要求 Key < End；点值还使用 Value，
// 允许空字节串值。
type Record struct {
	Seq   uint64
	Kind  Kind
	Key   []byte
	End   []byte
	Value []byte
}

// Covers 报告记录在键空间上是否覆盖 key。
func (r Record) Covers(key []byte) bool {
	switch r.Kind {
	case KindPointValue, KindPointDelete:
		return bytes.Equal(r.Key, key)
	case KindRangeDelete:
		return bytes.Compare(key, r.Key) >= 0 && bytes.Compare(key, r.End) < 0
	default:
		return false
	}
}

// Clone 返回深拷贝，避免调用方与包内共享底层数组。
func (r Record) Clone() Record {
	out := r
	out.Key = cloneBytes(r.Key)
	out.End = cloneBytes(r.End)
	if r.Value != nil {
		out.Value = append([]byte(nil), r.Value...)
	}
	return out
}

// ReadResult 是一次快照点查/扫描项的结果。
//
// Kind==KindMissing 表示缺失；KindPointValue 时 Value 为点值（可能长度为
// 零但非 nil）；KindPointDelete/KindRangeDelete 表示墓碑 null。
type ReadResult struct {
	Kind  Kind
	Value []byte
}

func (r ReadResult) equal(o ReadResult) bool {
	// 点删除与区间墓碑的读结果都是 null，二者语义等价；点值需比值，
	// 缺失（KindMissing）与空值必须严格区分。
	rNull := r.Kind == KindPointDelete || r.Kind == KindRangeDelete
	oNull := o.Kind == KindPointDelete || o.Kind == KindRangeDelete
	if rNull || oNull {
		return rNull && oNull
	}
	if r.Kind != o.Kind {
		return false
	}
	if r.Kind != KindPointValue {
		return true
	}
	return bytes.Equal(r.Value, o.Value)
}

// taggedRecord 给记录加上来源标记：其余层或某个被替换页。
type taggedRecord struct {
	Record
	fromReplaced bool
	pageID       uint32
}

// readAt 按快照语义求值：序号不晚于 snap 且覆盖 key 的记录中，最大序号胜出。
// 没有任何覆盖记录时返回 KindMissing。
func readAt(recs []taggedRecord, key []byte, snap uint64) ReadResult {
	best := -1
	for i := range recs {
		r := &recs[i]
		if r.Seq > snap || !r.Covers(key) {
			continue
		}
		if best == -1 || r.Seq > recs[best].Seq {
			best = i
		}
	}
	if best == -1 {
		return ReadResult{Kind: KindMissing}
	}
	win := recs[best]
	switch win.Kind {
	case KindPointValue:
		v := make([]byte, len(win.Value))
		copy(v, win.Value)
		return ReadResult{Kind: KindPointValue, Value: v}
	case KindPointDelete:
		return ReadResult{Kind: KindPointDelete}
	default:
		return ReadResult{Kind: KindRangeDelete}
	}
}

// Observation 是一个可观测点：在 Snapshot 快照下对 Key 的完整扫描项，
// Correct 为替换前的正确结果。
type Observation struct {
	Key      []byte
	Snapshot uint64
	Correct  ReadResult
}

// SeqInterval 是须保留的快照序号闭区间 [Lo, Hi]。
type SeqInterval struct {
	Lo uint64
	Hi uint64
}

// ReplacedPage 是压缩要替换掉的旧页。
type ReplacedPage struct {
	ID      uint32
	Records []Record
}

// CandidatePage 是允许生成的候选页。
//
// Encoding 为该页独立编码的物理字节；DictIDs 引用清单中的共享字典段；
// Records 为解码后的候选内容，只能原样复制旧点记录或截短旧区间墓碑。
type CandidatePage struct {
	ID       uint32
	Encoding []byte
	DictIDs  []uint32
	Records  []Record
}

// DictSegment 是共享字典段，Data 为其物理编码字节，多个候选页引用同一
// 字典段时物理字节只计费一次。
type DictSegment struct {
	ID   uint32
	Data []byte
}

// Manifest 是压缩选择问题的完整输入清单。
type Manifest struct {
	// OtherLayers 是不能改动的其余层记录。
	OtherLayers []Record
	// ReplacedPages 是要替换的页，其记录会被候选页整体取代。
	ReplacedPages []ReplacedPage
	// Candidates 是允许生成的候选页目录，至多 22 个。
	Candidates []CandidatePage
	// Dictionaries 是候选页可引用的共享字典段。
	Dictionaries []DictSegment
	// RetainIntervals 是须保留的合法快照序号闭区间之并。
	RetainIntervals []SeqInterval
	// LatestSnapshot 为最新快照序号，保证纳入观测。
	LatestSnapshot uint64
}

// maxCandidates 限制子集枚举规模：2^22。
const maxCandidates = 22

func cloneBytes(b []byte) []byte {
	if b == nil {
		return nil
	}
	return append([]byte(nil), b...)
}

// buildSnapshots 构造需要核验的快照集合。
//
// 读结果只在快照越过某条覆盖记录序号时变化，因此对每个保留闭区间取
// 其两端点以及落在其中的全部记录序号即可：区间内部相邻标记点之间不
// 存在序号变化点；区间端点保证区间内的首个合法快照（可能早于所有
// 记录序号）与末端都被核验。最后外加最新快照。
func buildSnapshots(m *Manifest) []uint64 {
	set := map[uint64]struct{}{}
	for _, iv := range m.RetainIntervals {
		set[iv.Lo] = struct{}{}
		set[iv.Hi] = struct{}{}
		for _, r := range m.oldRecords() {
			if r.Seq >= iv.Lo && r.Seq <= iv.Hi {
				set[r.Seq] = struct{}{}
			}
		}
	}
	set[m.LatestSnapshot] = struct{}{}
	out := make([]uint64, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// oldRecords 按其余层、被替换页的顺序返回全部旧记录。
func (m *Manifest) oldRecords() []Record {
	var out []Record
	out = append(out, m.OtherLayers...)
	for _, p := range m.ReplacedPages {
		out = append(out, p.Records...)
	}
	return out
}

// keyUniverse 返回完整范围扫描需要比对的全部键。
//
// 点记录键必须逐项出现；区间墓碑本身不是点，但完整范围扫描会扫到它
// 的边界与覆盖区域，因此把全部墓碑的下界也纳入键宇宙：下界是墓碑
// “恰好在该序号生效”的位置，漏掉它会让更旧外层值在边界复活而不被
// 任何点键观测捕获。所有比较都基于具体字节串，不枚举字节串键空间。
func keyUniverse(m *Manifest) [][]byte {
	set := map[string]struct{}{}
	for _, r := range m.oldRecords() {
		if r.Kind == KindPointValue || r.Kind == KindPointDelete || r.Kind == KindRangeDelete {
			set[string(r.Key)] = struct{}{}
		}
	}
	out := make([][]byte, 0, len(set))
	for k := range set {
		out = append(out, []byte(k))
	}
	sort.Slice(out, func(i, j int) bool { return bytes.Compare(out[i], out[j]) < 0 })
	return out
}

// baselineStore 构造替换前的带源记录存储。
func baselineStore(m *Manifest) []taggedRecord {
	var out []taggedRecord
	for _, r := range m.OtherLayers {
		out = append(out, taggedRecord{Record: r.Clone()})
	}
	for _, p := range m.ReplacedPages {
		for _, r := range p.Records {
			out = append(out, taggedRecord{Record: r.Clone(), fromReplaced: true, pageID: p.ID})
		}
	}
	return out
}

// observations 构造按（键字节序，快照序号）排序的完整观测列表。
func observations(m *Manifest, store []taggedRecord, snaps []uint64) []Observation {
	keys := keyUniverse(m)
	out := make([]Observation, 0, len(keys)*len(snaps))
	for _, k := range keys {
		for _, s := range snaps {
			out = append(out, Observation{
				Key:      append([]byte(nil), k...),
				Snapshot: s,
				Correct:  readAt(store, k, s),
			})
		}
	}
	return out
}
