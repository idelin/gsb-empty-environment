package compaction

import (
	"bytes"
	"fmt"
	"sort"
)

// Counterexample 是安装器发现的首个扫描反例，顺序按键字节再按快照序号。
type Counterexample struct {
	Key      []byte
	Snapshot uint64
	Expected ReadResult
	Got      ReadResult
}

// InstallReport 汇总安装校验结果。Counterexample 为 nil 表示逐观测一致。
type InstallReport struct {
	SelectedIDs         []uint32
	TotalBytes          int
	CheckedObservations int
	Counterexample      *Counterexample
}

// Verified 报告完整范围扫描是否与替换前逐项一致。
func (r *InstallReport) Verified() bool { return r.Counterexample == nil }

// Install 使用安装器自己的读游标验证清单与入选页：先独立解码全部候选页
// 与字典段物理字节，再以解码内容重建存储并对全部合法快照做完整范围
// 扫描。结构性问题（坏字节、悬空引用、内容与清单不符、清单非法）以
// error 返回；语义反例通过 InstallReport.Counterexample 返回。
func Install(m *Manifest, selectedIDs []uint32) (*InstallReport, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	candByID := map[uint32]CandidatePage{}
	for _, c := range m.Candidates {
		candByID[c.ID] = c
	}
	dictByID := map[uint32]DictSegment{}
	for _, d := range m.Dictionaries {
		dictByID[d.ID] = d
	}

	// 用独立读游标解码每个候选页的物理字节，并与清单声明逐字段比对。
	decoded := make([]CandidatePage, 0, len(selectedIDs))
	seenSel := map[uint32]struct{}{}
	for _, id := range selectedIDs {
		if _, dup := seenSel[id]; dup {
			return nil, fmt.Errorf("%w: candidate %d selected twice", ErrInvalidManifest, id)
		}
		seenSel[id] = struct{}{}
		c, ok := candByID[id]
		if !ok {
			return nil, fmt.Errorf("%w: selected candidate %d not in manifest", ErrInvalidManifest, id)
		}
		got, err := DecodePage(c.Encoding)
		if err != nil {
			return nil, fmt.Errorf("compaction: decode candidate %d: %w", id, err)
		}
		if got.ID != c.ID || !recordsEqual(got.Records, c.Records) || !u32Equal(got.DictIDs, c.DictIDs) {
			return nil, fmt.Errorf("%w: decoded bytes of candidate %d disagree with manifest", ErrInvalidManifest, id)
		}
		decoded = append(decoded, got)
		for _, did := range c.DictIDs {
			d, ok := dictByID[did]
			if !ok {
				return nil, fmt.Errorf("%w: candidate %d references unknown dictionary %d", ErrInvalidManifest, id, did)
			}
			seg, err := DecodeSegment(d.Data)
			if err != nil {
				return nil, fmt.Errorf("compaction: decode dictionary %d: %w", did, err)
			}
			if seg.ID != did {
				return nil, fmt.Errorf("%w: dictionary %d id mismatch in encoding", ErrInvalidManifest, did)
			}
		}
	}

	baseline := baselineStore(m)
	snaps := buildSnapshots(m)
	keys := keyUniverse(m)

	var installed []taggedRecord
	for _, r := range m.OtherLayers {
		installed = append(installed, taggedRecord{Record: r.Clone()})
	}
	for _, c := range decoded {
		for _, r := range c.Records {
			installed = append(installed, taggedRecord{Record: r.Clone(), fromReplaced: true})
		}
	}

	report := &InstallReport{
		SelectedIDs: append([]uint32(nil), selectedIDs...),
	}
	sort.Slice(report.SelectedIDs, func(i, j int) bool { return report.SelectedIDs[i] < report.SelectedIDs[j] })
	usedDict := map[uint32]struct{}{}
	for _, id := range report.SelectedIDs {
		c := candByID[id]
		report.TotalBytes += len(c.Encoding)
		for _, did := range c.DictIDs {
			usedDict[did] = struct{}{}
		}
	}
	for did := range usedDict {
		report.TotalBytes += len(dictByID[did].Data)
	}

	// 扫描顺序固定为键字节序、快照序号序，首个不一致即首个反例。
	for _, k := range keys {
		for _, s := range snaps {
			report.CheckedObservations++
			want := readAt(baseline, k, s)
			got := readAt(installed, k, s)
			if !got.equal(want) {
				report.Counterexample = &Counterexample{
					Key:      append([]byte(nil), k...),
					Snapshot: s,
					Expected: want,
					Got:      got,
				}
				return report, nil
			}
		}
	}
	return report, nil
}

func recordsEqual(a, b []Record) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		x, y := a[i], b[i]
		if x.Seq != y.Seq || x.Kind != y.Kind {
			return false
		}
		if !bytes.Equal(x.Key, y.Key) || !bytes.Equal(x.End, y.End) {
			return false
		}
		// 仅点值携带 Value；且 nil 与空切片在点值上语义不同，需区分比较。
		if x.Kind == KindPointValue {
			if (x.Value == nil) != (y.Value == nil) {
				return false
			}
			if !bytes.Equal(x.Value, y.Value) {
				return false
			}
		} else if x.Value != nil || y.Value != nil {
			return false
		}
	}
	return true
}

func u32Equal(a, b []uint32) bool {
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
