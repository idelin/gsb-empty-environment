// Package delegation 实现授权链（委托图）校验库。
//
// 授权可以无限向下委托，但子授权的动作集合、资源集合与有效期
// 都必须被父授权完全包含。撤销上游授权会使全部后代立即失效；
// 所有判定都基于事件时间戳，因此历史时点查询结果保持准确。
package delegation

import "time"

// Ref 指向一份授权的特定代次。
// Generation 为 0 时表示“解析到签发时刻最新的一代”。
type Ref struct {
	ID         string
	Generation int
}

// Key 返回该引用的规范字符串形式，如 "deploy#2"。
func (r Ref) Key() string {
	return keyOf(r.ID, r.Generation)
}

// Grant 描述一份授权。Parent 为 nil 表示根授权（直接由信任锚签发）。
type Grant struct {
	ID        string
	Issuer    string
	Subject   string
	Actions   []string
	Resources []string
	NotBefore time.Time
	NotAfter  time.Time
	Parent    *Ref
}

// Reason 是失效原因的机器可读代码。
type Reason string

const (
	ReasonNotFound              Reason = "NOT_FOUND"
	ReasonNotYetIssued          Reason = "NOT_YET_ISSUED"
	ReasonNotYetValid           Reason = "NOT_YET_VALID"
	ReasonExpired               Reason = "EXPIRED"
	ReasonRevoked               Reason = "REVOKED"
	ReasonSuperseded            Reason = "SUPERSEDED"
	ReasonParentMissing         Reason = "PARENT_MISSING"
	ReasonActionsExceedParent   Reason = "ACTIONS_EXCEED_PARENT"
	ReasonResourcesExceedParent Reason = "RESOURCES_EXCEED_PARENT"
	ReasonValidityExceedsParent Reason = "VALIDITY_EXCEEDS_PARENT"
	ReasonCycle                 Reason = "CYCLE"
)

// Result 是一次时点校验的结论。
// 当 Valid 为 false 时，Chain 给出从被校验授权到最近失效点的
// 最短原因链（如 ["leaf#1", "mid#2"]），Reason 为失效点的原因。
type Result struct {
	Valid  bool
	Reason Reason
	Chain  []string
}

// Error 实现 error 接口，便于直接作为错误返回。
func (r Result) Error() string {
	if r.Valid {
		return "delegation: valid"
	}
	s := "delegation: " + string(r.Reason) + " at "
	if len(r.Chain) > 0 {
		s += r.Chain[len(r.Chain)-1]
	}
	return s
}
