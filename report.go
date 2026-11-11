package netsec

import (
	"math/big"
	"time"
)

// Frequency 是一个已知敏感值在组内的频次统计。
type Frequency struct {
	Value string
	Count int
	// Ratio 是精确占比，分母口径由 Config.RatioDenominator 决定；
	// 当组内没有任何已知敏感值且采用 RatioOverKnown 口径时为 nil（分母为零）。
	Ratio *big.Rat
}

// GroupInfo 是一个泛化组的完整画像。
type GroupInfo struct {
	Key          GroupKey
	Size         int
	KnownCount   int
	UnknownCount int
	// Members 是组内全部记录 ID，按字典序排列。
	Members []string
	// Frequencies 按敏感值字典序排列；未知值不出现。
	Frequencies   []Frequency
	DistinctKnown int
}

// DiversityEvidence 证明多样性不足：给出最小代表集，
// 每个已知敏感值各取一条记录，其长度即实际种类数。
type DiversityEvidence struct {
	DistinctKnown int
	Required      int
	// RepresentativeMembers 按敏感值字典序每值取一条记录 ID。
	RepresentativeMembers []string
}

// RatioEvidence 给出单个越界敏感值的完整证据。
type RatioEvidence struct {
	Value       string
	Count       int
	Ratio       *big.Rat
	Limit       *big.Rat
	Members     []string
	Basis       RatioBasis
	Denominator int
}

// Violation 是一个违规组的最小证据。
type Violation struct {
	Key GroupKey
	// Size 是组内记录总数，用于识别最小违规组。
	Size int
	// Reasons 是命中的全部违规原因位掩码。
	Reasons Reason
	// Diversity 仅在 Reasons 含 ReasonDiversity 时非 nil。
	Diversity *DiversityEvidence
	// Ratios 仅在 Reasons 含 ReasonRatio 时非空，按值字典序排列。
	Ratios []RatioEvidence
}

// Report 是一次全量核验的结果。
type Report struct {
	GeneratedAt  time.Time
	TotalRecords int
	// Groups 按 GroupKey 字典序排列。
	Groups []GroupInfo
	// Violations 按组大小升序、组键字典序排列，首个即最小违规组。
	Violations []Violation
}

// Compliant 报告是否不存在任何违规组。
func (r *Report) Compliant() bool { return r != nil && len(r.Violations) == 0 }

// MinimalViolation 返回规模最小的违规组证据（同规模取组键字典序最小者），
// 无违规时返回 nil。
func (r *Report) MinimalViolation() *Violation {
	if r == nil || len(r.Violations) == 0 {
		return nil
	}
	return &r.Violations[0]
}
