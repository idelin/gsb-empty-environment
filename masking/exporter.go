package masking

import (
	"fmt"
	mathrand "math/rand"
	"time"
)

// Record 是一条待导出的数据记录（字段名 -> 值）。
type Record map[string]any

// Exporter 是合规导出内核：按角色与合规级别应用声明式策略。
type Exporter struct {
	policies *PolicySet
	// randSource 可注入以在测试中复现加噪结果；为 0 时使用随机种子。
	randSource int64
}

// NewExporter 创建导出器。
func NewExporter(ps *PolicySet) *Exporter {
	return &Exporter{policies: ps, randSource: time.Now().UnixNano()}
}

// WithRandSeed 固定加噪随机种子（用于可复现导出与测试）。
func (e *Exporter) WithRandSeed(seed int64) *Exporter {
	e.randSource = seed
	return e
}

// Export 按指定角色与合规级别导出记录集。
//
// 每次调用创建一个全新的假名化会话：本次导出内相同明文映射到相同假名
// （关联一致性），不同次导出之间假名不可关联。
// 未在策略中声明的字段默认不导出（默认拒绝，防止漏配导致明文外泄）。
func (e *Exporter) Export(role, level string, records []Record) ([]Record, error) {
	policy, err := e.policies.Lookup(role, level)
	if err != nil {
		return nil, err
	}
	pseudo, err := NewPseudonymizer()
	if err != nil {
		return nil, err
	}
	rnd := mathrand.New(mathrand.NewSource(e.randSource))
	transforms, err := policy.compile(pseudo, rnd)
	if err != nil {
		return nil, err
	}

	out := make([]Record, 0, len(records))
	for i, rec := range records {
		row := make(Record, len(transforms))
		for field, t := range transforms {
			value, ok := rec[field]
			if !ok {
				return nil, fmt.Errorf("记录 %d 缺少策略声明的字段 %q", i, field)
			}
			row[field] = t(value)
		}
		out = append(out, row)
	}
	return out, nil
}
