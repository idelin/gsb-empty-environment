// Package netsec 在查询执行过程中传播“不同主体支持集”证明。
//
// 数据安全引擎允许由足够多不同主体（principal，例如自然人、组织）共同
// 支持的聚合值去掉敏感标签。本包不按行数、复制次数或连接命中次数计数，
// 只按不同主体 ID 的数量计数：
//
//   - 复制（投影、重复值）共享同一份证明，不增加主体数。
//   - 连接把两侧贡献的主体集合取并集，同一主体只计一次。
//   - 聚合把被聚合值的证明取并集，去标判定只在最终结果上进行。
//   - 过滤不会缩小保留行的证明，但会移除整行；去标与否始终按保留下来的
//     值自带的证明重新评估，不存在“去标后永久放行”的粘性标志。
package netsec

import (
	"fmt"
	"sort"
)

// Principal 标识一个独立的数据主体。是否去标只按不同主体的数量决定。
type Principal string

// Proof 是某个值的不同主体支持集。
//
// Proof 在构造完成后视为不可变：所有变换都返回新的 Proof，因此同一个
// Proof 可以被多个复制出来的值安全共享，且共享不会让主体数翻倍。
type Proof struct {
	support map[Principal]struct{}
}

// Empty 返回不含任何主体的空证明。空证明支持的值在任何正阈值下都不能去标。
func Empty() Proof {
	return Proof{support: map[Principal]struct{}{}}
}

// Of 用一个或多个不同主体构造证明；重复传入的同一主体只计一次。
func Of(principals ...Principal) Proof {
	p := Proof{support: make(map[Principal]struct{}, len(principals))}
	for _, principal := range principals {
		p.support[principal] = struct{}{}
	}
	return p
}

// Union 返回多个证明的不同主体并集。任一证明包含某主体，结果即包含该主体，
// 因此共享来源（同一主体经多条路径到达）不会被重复计数。
func Union(proofs ...Proof) Proof {
	merged := Empty()
	for _, proof := range proofs {
		for principal := range proof.support {
			merged.support[principal] = struct{}{}
		}
	}
	return merged
}

// Count 返回支持集中不同主体的数量。
func (p Proof) Count() int {
	return len(p.support)
}

// Supports 报告给定主体是否在支持集中。
func (p Proof) Supports(principal Principal) bool {
	_, ok := p.support[principal]
	return ok
}

// Principals 按确定顺序返回支持集中的主体，便于测试与审计。
func (p Proof) Principals() []Principal {
	out := make([]Principal, 0, len(p.support))
	for principal := range p.support {
		out = append(out, principal)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Value 是查询宿主在关系中传递的带证明值。
//
// 复制一个 Value（例如投影到多列、生成重复行）应直接复用该 Value 或其
// Proof；证明按主体集合而非值的出现次数计数，所以复制不增加人数。
type Value struct {
	Data  any
	Proof Proof
}

// NewValue 用数据与支持它的主体构造带证明值。
func NewValue(data any, principals ...Principal) Value {
	return Value{Data: data, Proof: Of(principals...)}
}

// WithProof 用给定证明包装数据，用于继承上游证明而不改动其支持集。
func WithProof(data any, proof Proof) Value {
	return Value{Data: data, Proof: proof}
}

// Copy 返回原值的一个副本，证明按值复制底层集合但主体不变。
// 复制前后主体数完全相同，不会因为多了一行而“凑人数”。
func (v Value) Copy() Value {
	return Value{Data: v.Data, Proof: Union(v.Proof)}
}

// Policy 是去标策略：仅当值由至少 K 个不同主体支持时才允许去标。
type Policy struct {
	// K 是去标所需的最少不同主体数，必须为正数。
	K int
}

// NewPolicy 构造去标策略并拒绝非正阈值。
func NewPolicy(k int) (Policy, error) {
	if k <= 0 {
		return Policy{}, fmt.Errorf("netsec: 去标阈值 K 必须为正数，得到 %d", k)
	}
	return Policy{K: k}, nil
}

// CanRelease 报告值在该策略下能否去掉敏感标签。
// 判定依据始终是值自带证明中的不同主体数，而不是行数或复制次数。
func (c Policy) CanRelease(v Value) bool {
	return v.Proof.Count() >= c.K
}

// Sensitive 是 CanRelease 的反向判定：主体不足时仍带敏感标签。
func (c Policy) Sensitive(v Value) bool {
	return !c.CanRelease(v)
}
