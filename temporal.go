// Package temporalfx 实现可审计的多时区汇率双时态折算器。
//
// 每条汇率发布携带两个时间维度：
//   - Effective（牌价生效时间）：汇率在业务上开始有效的时刻；
//   - Known（知悉时间）：发布中心实际知悉（收到）该牌价的时刻。
//
// 折算采用“按交易发生时刻最近已知有效汇率”语义：
// 在知悉时间 asOf 视角下，对所有 Effective <= txnTime 且 Known <= asOf
// 的版本，先生效时间最新者优先；生效时间相同（延迟发布/紧急更正）时，
// 知悉时间最新者优先；再相同则按发布序号（后发布覆盖先发布）。
package temporalfx

import (
	"fmt"
	"math/big"
	"sort"
	"time"
)

// PublishKind 标识一次发布的性质。
type PublishKind int

const (
	// Initial 常规发布（含延迟发布）。
	Initial PublishKind = iota
	// Correction 对同一生效时刻牌价的紧急更正。
	Correction
)

func (k PublishKind) String() string {
	if k == Correction {
		return "correction"
	}
	return "initial"
}

// RateVersion 是一条不可变的汇率发布记录。
type RateVersion struct {
	Seq       int       // 发布序号，由 Store 单调分配，用于最终决胜
	Pair      string    // 货币对，如 "USD/CNY"
	Effective time.Time // 牌价生效时间（UTC 存储）
	Known     time.Time // 发布中心知悉时间（UTC 存储）
	Value     *big.Rat  // 1 单位外币折合本币数量
	Kind      PublishKind
	Note      string
}

// VersionID 返回该版本的稳定标识，用于审计留痕。
func (v RateVersion) VersionID() string {
	return fmt.Sprintf("%s#%d", v.Pair, v.Seq)
}

// Store 是只追加的汇率发布日志，并记录全部折算审计。
type Store struct {
	versions    []RateVersion
	conversions []Conversion
}

// NewStore 创建空存储。
func NewStore() *Store { return &Store{} }

// Publish 登记一次汇率发布（延迟发布与更正均通过此入口），返回版本号。
func (s *Store) Publish(pair string, effective, known time.Time, value string, kind PublishKind, note string) (RateVersion, error) {
	rat, ok := new(big.Rat).SetString(value)
	if !ok {
		return RateVersion{}, fmt.Errorf("invalid rate value %q", value)
	}
	if rat.Sign() <= 0 {
		return RateVersion{}, fmt.Errorf("rate value must be positive: %q", value)
	}
	v := RateVersion{
		Seq:       len(s.versions) + 1,
		Pair:      pair,
		Effective: effective.UTC(),
		Known:     known.UTC(),
		Value:     rat,
		Kind:      kind,
		Note:      note,
	}
	s.versions = append(s.versions, v)
	return v, nil
}

// RateAt 返回在知悉时间 asOf 视角下、对交易时刻 txnTime 最近已知有效的汇率版本。
func (s *Store) RateAt(pair string, txnTime, asOf time.Time) (RateVersion, bool) {
	txn := txnTime.UTC()
	know := asOf.UTC()
	var best *RateVersion
	for i := range s.versions {
		v := &s.versions[i]
		if v.Pair != pair {
			continue
		}
		if v.Effective.After(txn) || v.Known.After(know) {
			continue
		}
		if best == nil || supersedes(*v, *best) {
			best = v
		}
	}
	if best == nil {
		return RateVersion{}, false
	}
	return *best, true
}

// supersedes 判定 a 是否比 b 更“新”：先生效时间，再知悉时间，再发布序号。
func supersedes(a, b RateVersion) bool {
	if !a.Effective.Equal(b.Effective) {
		return a.Effective.After(b.Effective)
	}
	if !a.Known.Equal(b.Known) {
		return a.Known.After(b.Known)
	}
	return a.Seq > b.Seq
}

// Conversion 是一笔折算的完整审计记录。
type Conversion struct {
	Pair          string
	ForeignAmount *big.Rat    // 外币金额
	LocalAmount   *big.Rat    // 折算出的本币金额
	Rate          RateVersion // 折算依据的汇率版本
	TxnTime       time.Time   // 交易发生时刻
	AsOf          time.Time   // 查询所采用的知悉时间
	Basis         string      // 人可读的折算依据说明
}

// Convert 按交易时刻 txnTime、知悉时间 asOf 折算外币金额为本币，
// 并将结果追加到审计日志。amount 为十进制字符串，避免浮点误差。
func (s *Store) Convert(pair string, amount string, txnTime, asOf time.Time) (Conversion, error) {
	v, ok := s.RateAt(pair, txnTime, asOf)
	if !ok {
		return Conversion{}, fmt.Errorf("no known-effective rate for %s at txn=%s asOf=%s",
			pair, txnTime.UTC().Format(time.RFC3339), asOf.UTC().Format(time.RFC3339))
	}
	amt, ok := new(big.Rat).SetString(amount)
	if !ok {
		return Conversion{}, fmt.Errorf("invalid amount %q", amount)
	}
	local := new(big.Rat).Mul(amt, v.Value)
	c := Conversion{
		Pair:          pair,
		ForeignAmount: amt,
		LocalAmount:   local,
		Rate:          v,
		TxnTime:       txnTime.UTC(),
		AsOf:          asOf.UTC(),
		Basis: fmt.Sprintf("%s %s @ %s (effective %s, known %s, %s)",
			amount, pair, Decimal(v.Value, 6),
			v.Effective.Format(time.RFC3339), v.Known.Format(time.RFC3339), v.Kind),
	}
	s.conversions = append(s.conversions, c)
	return c, nil
}

// AuditLog 返回全部折算记录（按发生顺序），供审计导出。
func (s *Store) AuditLog() []Conversion {
	out := make([]Conversion, len(s.conversions))
	copy(out, s.conversions)
	return out
}

// Versions 返回某货币对的全部发布版本，按生效/知悉/序号排序，供审计核查。
func (s *Store) Versions(pair string) []RateVersion {
	var out []RateVersion
	for _, v := range s.versions {
		if v.Pair == pair {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return supersedes(out[j], out[i]) })
	return out
}

// ParseIn 按指定时区解析 "2006-01-02 15:04" 形式的时间，便于跨时区录入。
func ParseIn(loc *time.Location, value string) (time.Time, error) {
	t, err := time.ParseInLocation("2006-01-02 15:04", value, loc)
	if err != nil {
		return time.Time{}, err
	}
	return t.UTC(), nil
}

// Decimal 将精确有理数格式化为指定位数的十进制字符串，用于展示与审计。
func Decimal(r *big.Rat, places int) string {
	return r.FloatString(places)
}
