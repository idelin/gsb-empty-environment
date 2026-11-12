package queryid

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// Adjacency 是差分隐私的邻接定义，属于机制身份的一部分。
type Adjacency string

const (
	// AddRemove 增删邻接：相邻数据集相差一行。
	AddRemove Adjacency = "add-remove"
	// Substitute 替换邻接：相邻数据集有一行被替换。
	Substitute Adjacency = "substitute"
)

// ClipRule 描述贡献裁剪规则；裁剪界用有理数精确表示。
type ClipRule struct {
	Kind  string // 如 "l1-contrib"、"value-cap"
	Bound Rat
}

func (c ClipRule) key() string {
	return lengthPrefixed(c.Kind) + "@" + c.Bound.Key()
}

// Mechanism 是完全确定一次加噪结果的机制描述。
// 只有所有字段都相同的查询才允许共享缓存结果。
type Mechanism struct {
	Agg        string    // 聚合种类："sum"、"count"、"mean" 等
	Params     []Rat     // 有理聚合参数（如 ε、阈值），顺序敏感
	Adj        Adjacency // 邻接定义
	Clip       ClipRule  // 裁剪规则
	NoiseEpoch uint64    // 噪声代次：换一代噪声即换一个身份
}

// Identity 是一次匿名查询的完整缓存身份：规范化后的域 + 机制。
type Identity struct {
	Domain CanonSet
	Mech   Mechanism
}

// NewIdentity 规范化域表达式并组装查询身份。
func NewIdentity(domain *SetExpr, mech Mechanism) (Identity, error) {
	if domain == nil {
		return Identity{}, errors.New("queryid: nil domain expression")
	}
	c, err := domain.Canonical()
	if err != nil {
		return Identity{}, err
	}
	return Identity{Domain: c, Mech: mech}, nil
}

// Key 返回身份的规范字符串，字段均带长度前缀，无拼接歧义。
func (id Identity) Key() string {
	var b strings.Builder
	b.WriteString("qid/v1|agg=")
	b.WriteString(lengthPrefixed(id.Mech.Agg))
	b.WriteString("|params=")
	for i, p := range id.Mech.Params {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(p.Key())
	}
	b.WriteString("|adj=")
	b.WriteString(lengthPrefixed(string(id.Mech.Adj)))
	b.WriteString("|clip=")
	b.WriteString(id.Mech.Clip.key())
	fmt.Fprintf(&b, "|epoch=%d|", id.Mech.NoiseEpoch)
	b.WriteString(id.Domain.Key())
	return b.String()
}

// CacheKey 返回适合直接做缓存键的 SHA-256 十六进制摘要。
func (id Identity) CacheKey() string {
	sum := sha256.Sum256([]byte(id.Key()))
	return hex.EncodeToString(sum[:])
}

// SharesResultWith 报告两个身份是否完全同机制、同域，
// 即是否允许共享同一份加噪结果。
func (id Identity) SharesResultWith(o Identity) bool {
	return id.Key() == o.Key()
}

func lengthPrefixed(s string) string {
	return fmt.Sprintf("%d:%s", len(s), s)
}
