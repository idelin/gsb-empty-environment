package netsec

import (
	"fmt"
	"math/big"
	"slices"
	"strings"
)

// UnknownSensitive 是敏感属性“未知”的哨兵值（空字符串）。
// 未知值代表真实存在的记录，但语义上没有可泄漏的敏感类别：
// 它不参与 L 种敏感值计数，也不参与占比分子，绝不能被当成一个普通类别凑数。
const UnknownSensitive = ""

// Reason 是组违规原因的位掩码。
type Reason uint8

const (
	// ReasonNone 表示组通过全部检查。
	ReasonNone Reason = 0
	// ReasonGroupSize 表示组未达到可选的最小组大小 k。
	ReasonGroupSize Reason = 1 << iota
	// ReasonDiversity 表示组内已知敏感值种类少于 L。
	ReasonDiversity
	ReasonRatio
)

// String 以助记形式返回违规原因（合规时返回 "none"）。
func (r Reason) String() string {
	if r == ReasonNone {
		return "none"
	}
	var parts []string
	if r&ReasonGroupSize != 0 {
		parts = append(parts, "group-size")
	}
	if r&ReasonDiversity != 0 {
		parts = append(parts, "diversity")
	}
	if r&ReasonRatio != 0 {
		parts = append(parts, "ratio")
	}
	return strings.Join(parts, "+")
}

// RatioBasis 选择敏感值占比的分母口径。
type RatioBasis uint8

const (
	// RatioOverAll 以全组记录数（含未知敏感值记录）为分母。
	// 未知记录摊薄事实占比，是更严格的默认口径：阈值必须对全体成立。
	RatioOverAll RatioBasis = iota
	// RatioOverKnown 仅以已知敏感值记录数为分母（经典 l-多样性口径）。
	RatioOverKnown
)

// Threshold 以精确有理数 p/q 表达占比上限，避免浮点误差影响边界判定。
type Threshold = big.Rat

// NewThreshold 返回表示 num/den 的有理阈值，den 必须为正数。
func NewThreshold(num, den int64) (*big.Rat, error) {
	if den <= 0 {
		return nil, fmt.Errorf("netsec: 阈值分母必须为正数，收到 %d", den)
	}
	if num < 0 {
		return nil, fmt.Errorf("netsec: 阈值分子不能为负数，收到 %d", num)
	}
	return big.NewRat(num, den), nil
}

// ExactShare 返回 count/total 的精确有理占比，total 必须为正数。
func ExactShare(count, total int) *big.Rat {
	if total <= 0 {
		panic(fmt.Sprintf("netsec: 占比分母必须为正数，收到 %d", total))
	}
	return big.NewRat(int64(count), int64(total))
}

// Config 描述一次分组核验的全部静态参数。
type Config struct {
	// QuasiIDs 按归组顺序列出准标识属性，名称需与 Tree.Name 一一对应。
	QuasiIDs []string
	// Trees 与 QuasiIDs 同序的泛化树。
	Trees []*Tree
	// Levels 是每个准标识向上泛化的步数，与 QuasiIDs 同序。
	Levels []int
	// L 是每组至少需要的已知敏感值种类数，必须 >= 1。
	L int
	// MinGroupSize 是可选的 k-匿名最小组大小，0 表示不检查。
	MinGroupSize int
	// MaxRatio 是单个敏感值占比的有理上限，nil 表示不检查占比。
	// 边界语义：占比 == MaxRatio 视为合规，严格大于才算违规。
	MaxRatio *big.Rat
	// RatioDenominator 选择占比分母口径，零值为 RatioOverAll。
	RatioDenominator RatioBasis
	// UnknownSentinel 自定义“未知敏感值”哨兵，零值为空字符串。
	UnknownSentinel string
}

// Record 是一条待发布记录。
type Record struct {
	// ID 是记录的稳定唯一标识，撤回时按 ID 定位。
	ID string
	// QuasiValues 按 Config.QuasiIDs 顺序给出具体准标识值（必须是树中节点）。
	QuasiValues []string
	// Sensitive 是敏感属性值；等于未知哨兵表示值未知。
	Sensitive string
}

// GroupKey 是一组记录共享的泛化后准标识向量。
type GroupKey struct {
	attrs  []string
	joined string
}

// Values 返回泛化后各准标识值的副本，顺序与 Config.QuasiIDs 一致。
func (k GroupKey) Values() []string {
	return slices.Clone(k.attrs)
}

// String 返回以 NUL 连接的确定性文本形式，可用于宿主侧映射键。
func (k GroupKey) String() string { return k.joined }
