package taskpkg

import "math/big"

// 问题规模上限。
const (
	MaxLights         = 8  // 最多 8 盏灯
	MaxExposureCands  = 3  // 每灯最多 3 个正有理曝光候选
	MaxPixels         = 32 // 最多 32 个像素
	RequiredTripleLen = 3  // 三灯组
)

// Observation 是单个像素在单盏灯下的一次亮度观测。
type Observation struct {
	// Brightness 为观测亮度，必须为非负有理数。
	Brightness *big.Rat
	// Saturated 为 true 表示该观测饱和，必须显式排除、不参与拟合。
	Saturated bool
}

// RejectReason 说明观测被拒绝的原因。
type RejectReason string

const (
	// RejectSaturated 表示观测因饱和被排除。
	RejectSaturated RejectReason = "saturated"
	// RejectResidual 表示观测未饱和，但在最终选定模型下残差超过阈值。
	RejectResidual RejectReason = "residual"
)

// RejectedObservation 记录一条被排除在一致集合之外的观测。
type RejectedObservation struct {
	Pixel      int
	Light      int
	Reason     RejectReason
	Brightness *big.Rat
	Prediction *big.Rat
	Residual   *big.Rat
	Threshold  *big.Rat
}

// PixelResult 是单个像素在全图选定曝光向量下的求解结果。
type PixelResult struct {
	Pixel int
	// G 为精确有理的 g = 反照率 * 法线（零向量表示该像素不可解）。
	G [3]*big.Rat
	// Normal 为单位法线；不可解时为零向量。
	Normal [3]float64
	// Albedo 为反照率标量 ‖g‖；不可解时为 0。
	Albedo float64
	// Triple 是胜出候选使用的三灯 ID（升序）；不可解时为 nil。
	Triple [3]int
	// TripleValid 标记 Triple 是否有效。
	TripleValid bool
	// Consistent 为一致（残差不超过阈值）的非饱和观测数。
	Consistent int
	// SquaredError 为全部非饱和观测上的平方误差（精确有理）。
	SquaredError *big.Rat
	// Solved 为 false 表示该像素无法由任何满秩三灯组解出。
	Solved bool
}

// Problem 是一次联合校准的输入。
type Problem struct {
	// Lights[k] 为第 k 盏灯的已知三维有理光向量，长度即灯数（3..8）。
	Lights [][3]*big.Rat
	// ExposureCandidates[k] 为第 k 盏灯的正有理曝光候选；首灯必须且
	// 只能包含 1（首灯曝光固定为 1）。
	ExposureCandidates [][]*big.Rat
	// Observations[p][k] 为像素 p 在灯 k 下的观测；行宽与灯数一致。
	Observations [][]Observation
	// ResidualThreshold 为非负有理残差阈值（绝对亮度差）。
	ResidualThreshold *big.Rat
	// Budget 为允许评估的满秩三灯组数量上界；0 表示不设预算。
	Budget int64
}

// Result 是联合校准的输出。
type Result struct {
	// Exposure 为全图共用的曝光向量（精确有理）。
	Exposure []*big.Rat
	// ExposureIndex 为曝光向量在字典序枚举中的索引（从 0 开始）。
	ExposureIndex int
	Pixels        []PixelResult
	Rejected      []RejectedObservation
	// TotalConsistent 为全图一致观测总数。
	TotalConsistent int
	// TotalSquaredError 为全图平方误差总和（精确有理）。
	TotalSquaredError *big.Rat
	// Evaluations 为实际评估的满秩三灯组数量。
	Evaluations int64
	// TotalEvaluations 为完整枚举所需的满秩三灯组评估总数。
	TotalEvaluations int64
	// NotOptimal 为 true 表示预算提前耗尽，返回的是当前最优可行解。
	NotOptimal bool
}
