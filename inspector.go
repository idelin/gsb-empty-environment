package checksumdiff

import "time"

// DiffKind 描述单行不一致的类型。
type DiffKind string

const (
	MissingInTarget DiffKind = "MISSING_IN_TARGET" // 目标库丢数
	ExtraInTarget   DiffKind = "EXTRA_IN_TARGET"   // 目标库多出未同步的行
	Modified        DiffKind = "MODIFIED"          // 行内容被篡改/不一致
)

// RowDiff 定位到具体主键的不一致记录。
type RowDiff struct {
	ID     int64
	Kind   DiffKind
	Source Row // Kind 为 ExtraInTarget 时为零值
	Target Row // Kind 为 MissingInTarget 时为零值
}

// BucketDiff 是一个分桶内的所有行级差异。
type BucketDiff struct {
	Bucket int
	Diffs  []RowDiff
}

// Report 是一个巡检周期的比对结果。
type Report struct {
	Equal    bool
	SrcRoot  uint64
	DstRoot  uint64
	Buckets  []BucketDiff // 仅包含签名不一致的分桶
	Duration time.Duration
}

// AllDiffs 汇总报告中所有行级差异。
func (r *Report) AllDiffs() []RowDiff {
	var out []RowDiff
	for _, b := range r.Buckets {
		out = append(out, b.Diffs...)
	}
	return out
}

// Inspector 是源库/目标库一致性在线巡检器。
// 每个周期先比根签名（O(1)），不一致时下钻到分桶签名（O(NumBuckets)），
// 最后只对失配分桶做行级 diff，精确定位主键。
type Inspector struct {
	Src *Store
	Dst *Store
}

func NewInspector(src, dst *Store) *Inspector {
	return &Inspector{Src: src, Dst: dst}
}

// Inspect 执行一个巡检周期。
func (in *Inspector) Inspect() *Report {
	start := time.Now()
	rep := &Report{SrcRoot: in.Src.Root(), DstRoot: in.Dst.Root()}
	if rep.SrcRoot == rep.DstRoot {
		rep.Equal = true
		rep.Duration = time.Since(start)
		return rep
	}
	for b := 0; b < NumBuckets; b++ {
		if in.Src.BucketSig(b) == in.Dst.BucketSig(b) {
			continue
		}
		rep.Buckets = append(rep.Buckets, in.diffBucket(b))
	}
	rep.Duration = time.Since(start)
	return rep
}

// diffBucket 对单个失配分桶做行级比对，定位到主键。
func (in *Inspector) diffBucket(b int) BucketDiff {
	srcRows := in.Src.BucketRows(b)
	dstRows := in.Dst.BucketRows(b)
	dstByID := make(map[int64]Row, len(dstRows))
	for _, r := range dstRows {
		dstByID[r.ID] = r
	}
	bd := BucketDiff{Bucket: b}
	for _, sr := range srcRows {
		dr, ok := dstByID[sr.ID]
		if !ok {
			bd.Diffs = append(bd.Diffs, RowDiff{ID: sr.ID, Kind: MissingInTarget, Source: sr})
			continue
		}
		if sr.Hash() != dr.Hash() {
			bd.Diffs = append(bd.Diffs, RowDiff{ID: sr.ID, Kind: Modified, Source: sr, Target: dr})
		}
		delete(dstByID, sr.ID)
	}
	for _, dr := range dstByID {
		bd.Diffs = append(bd.Diffs, RowDiff{ID: dr.ID, Kind: ExtraInTarget, Target: dr})
	}
	return bd
}
