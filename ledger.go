// Package kpi 实现员工绩效考评时态指标账本。
//
// 跨考评周期的 KPI 目标值/权重按版本管理：每次战略调整产生一个
// 追加式版本，权重在考核周期内按天区间 [StartDay, EndDay) 生效。
// 支持在任意历史版本快照下推演季度末加权得分，并记录版本间影响。
package kpi

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
)

// Entry 表示某个 KPI 在考核周期内一段天区间上的权重。
// 天为 1 起始，区间为左闭右开 [StartDay, EndDay)。
type Entry struct {
	KPI      string
	Weight   float64
	StartDay int
	EndDay   int
}

// Version 是一次战略调整产生的不可变快照，带责任人签名。
type Version struct {
	ID          int
	Author      string // 指标变更责任人
	Reason      string // 变更原因
	RecordedDay int    // 在考核周期第几天登记（可追溯生效）
	Entries     []Entry
	Signature   string // 责任人版本签名（内容哈希）
}

// WeightChange 描述某 KPI 在两个版本间有效权重的变化。
type WeightChange struct {
	KPI        string
	FromWeight float64
	ToWeight   float64
}

// Impact 记录一次版本变更对权重与季度末得分的影响。
type Impact struct {
	FromVersion   int
	ToVersion     int
	WeightChanges []WeightChange
	ScoreBefore   float64
	ScoreAfter    float64
	ScoreDelta    float64
}

// Ledger 是追加式的时态指标账本。
type Ledger struct {
	QuarterDays int
	versions    []Version
}

// NewLedger 创建账本，quarterDays 为考核周期总天数。
func NewLedger(quarterDays int) *Ledger {
	return &Ledger{QuarterDays: quarterDays}
}

// AddVersion 追加一个新版本并生成责任人签名，返回该版本。
func (l *Ledger) AddVersion(author, reason string, recordedDay int, entries []Entry) Version {
	v := Version{
		ID:          len(l.versions) + 1,
		Author:      author,
		Reason:      reason,
		RecordedDay: recordedDay,
		Entries:     append([]Entry(nil), entries...),
	}
	v.Signature = sign(v)
	l.versions = append(l.versions, v)
	return v
}

// sign 对版本内容（责任人、原因、登记日、全部条目）计算 SHA-256 签名。
func sign(v Version) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s|%s|%d|", v.Author, v.Reason, v.RecordedDay)
	for _, e := range v.Entries {
		fmt.Fprintf(h, "%s=%.10f@[%d,%d);", e.KPI, e.Weight, e.StartDay, e.EndDay)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// Versions 返回全部历史版本（按登记顺序）。
func (l *Ledger) Versions() []Version {
	return append([]Version(nil), l.versions...)
}

// Version 按 ID 查询历史版本快照。
func (l *Ledger) Version(id int) (Version, bool) {
	if id < 1 || id > len(l.versions) {
		return Version{}, false
	}
	return l.versions[id-1], true
}

// EffectiveWeights 计算指定版本快照下每个 KPI 的时间加权有效权重：
// 有效权重 = Σ(权重 × 生效天数) / 周期总天数。
func (l *Ledger) EffectiveWeights(versionID int) (map[string]float64, error) {
	v, ok := l.Version(versionID)
	if !ok {
		return nil, fmt.Errorf("kpi: version %d not found", versionID)
	}
	weights := make(map[string]float64)
	for _, e := range v.Entries {
		if e.EndDay <= e.StartDay {
			return nil, fmt.Errorf("kpi: invalid interval [%d,%d) for %s", e.StartDay, e.EndDay, e.KPI)
		}
		weights[e.KPI] += e.Weight * float64(e.EndDay-e.StartDay) / float64(l.QuarterDays)
	}
	return weights, nil
}

// Score 在指定版本快照下，用实际得分推演季度末最终加权得分。
func (l *Ledger) Score(versionID int, actuals map[string]float64) (float64, error) {
	weights, err := l.EffectiveWeights(versionID)
	if err != nil {
		return 0, err
	}
	var score float64
	for kpi, w := range weights {
		score += w * actuals[kpi]
	}
	return score, nil
}

// Impact 计算从 from 版本变更到 to 版本的影响：各 KPI 有效权重变化
// 以及给定实际得分下的季度末得分差。
func (l *Ledger) Impact(fromID, toID int, actuals map[string]float64) (Impact, error) {
	fromW, err := l.EffectiveWeights(fromID)
	if err != nil {
		return Impact{}, err
	}
	toW, err := l.EffectiveWeights(toID)
	if err != nil {
		return Impact{}, err
	}
	before, err := l.Score(fromID, actuals)
	if err != nil {
		return Impact{}, err
	}
	after, err := l.Score(toID, actuals)
	if err != nil {
		return Impact{}, err
	}

	kpiSet := make(map[string]bool)
	for k := range fromW {
		kpiSet[k] = true
	}
	for k := range toW {
		kpiSet[k] = true
	}
	kpis := make([]string, 0, len(kpiSet))
	for k := range kpiSet {
		kpis = append(kpis, k)
	}
	sort.Strings(kpis)

	imp := Impact{FromVersion: fromID, ToVersion: toID, ScoreBefore: before, ScoreAfter: after, ScoreDelta: after - before}
	for _, k := range kpis {
		if fromW[k] != toW[k] {
			imp.WeightChanges = append(imp.WeightChanges, WeightChange{KPI: k, FromWeight: fromW[k], ToWeight: toW[k]})
		}
	}
	return imp, nil
}
