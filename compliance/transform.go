package compliance

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"math/rand"
)

// Context 单次导出的上下文：携带导出密钥与确定性随机源。
// 同一次导出共享同一 Context，保证假名/加噪在导出内关联一致。
type Context struct {
	key []byte
	rng *rand.Rand
}

// Transform 声明式字段转换接口。
type Transform interface {
	// Apply 对字段原始值执行转换，返回导出值。
	Apply(value interface{}, ctx *Context) interface{}
	// Name 转换名称，用于审计与测试断言。
	Name() string
}

// Pass 原样透传（有权限查看明文）。
type Pass struct{}

func (Pass) Name() string { return "pass" }
func (Pass) Apply(v interface{}, _ *Context) interface{} {
	return v
}

// Deny 默认拒绝：字段以固定占位符输出，不泄露任何信息。
type Deny struct{}

func (Deny) Name() string { return "deny" }
func (Deny) Apply(_ interface{}, _ *Context) interface{} {
	return "[REDACTED]"
}

// Mask 掩码：保留前 Head 位、后 Tail 位，中间以 Pad 替换。
// 例：手机号 13812345678 经 Mask{Head:3, Tail:4} -> 138****5678。
type Mask struct {
	Head int
	Tail int
	Pad  rune
}

func (m Mask) Name() string { return fmt.Sprintf("mask(%d,%d)", m.Head, m.Tail) }

func (m Mask) Apply(v interface{}, _ *Context) interface{} {
	s, ok := v.(string)
	if !ok {
		return Deny{}.Apply(v, nil)
	}
	pad := m.Pad
	if pad == 0 {
		pad = '*'
	}
	r := []rune(s)
	if len(r) <= m.Head+m.Tail {
		// 长度不足以保留首尾时全部掩码，避免泄露。
		out := make([]rune, len(r))
		for i := range out {
			out[i] = pad
		}
		return string(out)
	}
	out := make([]rune, 0, len(r))
	out = append(out, r[:m.Head]...)
	for i := 0; i < len(r)-m.Head-m.Tail; i++ {
		out = append(out, pad)
	}
	out = append(out, r[len(r)-m.Tail:]...)
	return string(out)
}

// HashPseudonym 哈希假名化：HMAC-SHA256(导出密钥, 字段名+原始值)。
// 同一导出内相同明文得到相同假名（关联一致性）；
// 不同导出使用不同密钥，假名不可跨导出关联。
type HashPseudonym struct {
	Prefix string
}

func (HashPseudonym) Name() string { return "hash-pseudonym" }

func (h HashPseudonym) Apply(v interface{}, ctx *Context) interface{} {
	s, ok := v.(string)
	if !ok {
		return Deny{}.Apply(v, ctx)
	}
	mac := hmac.New(sha256.New, ctx.key)
	mac.Write([]byte(s))
	sum := hex.EncodeToString(mac.Sum(nil))
	prefix := h.Prefix
	if prefix == "" {
		prefix = "PSEUDO"
	}
	return fmt.Sprintf("%s-%s", prefix, sum[:10])
}

// Bucket 区间分桶：将数值落入 [Edges[i], Edges[i+1]) 区间并输出区间标签。
// 例：薪资 23000，Edges=[0,10000,15000,20000,25000,30000] -> "20000-25000"。
type Bucket struct {
	Edges []float64
}

func (Bucket) Name() string { return "bucket" }

func (b Bucket) Apply(v interface{}, _ *Context) interface{} {
	f, ok := toFloat(v)
	if !ok || len(b.Edges) < 2 {
		return Deny{}.Apply(v, nil)
	}
	if f < b.Edges[0] {
		return fmt.Sprintf("<%v", b.Edges[0])
	}
	for i := 0; i < len(b.Edges)-1; i++ {
		if f >= b.Edges[i] && f < b.Edges[i+1] {
			return fmt.Sprintf("%v-%v", b.Edges[i], b.Edges[i+1])
		}
	}
	return fmt.Sprintf(">=%v", b.Edges[len(b.Edges)-1])
}

// LaplaceNoise 差分加噪：对数值叠加拉普拉斯噪声（尺度 Scale）。
// 噪声由导出密钥派生的确定性随机源生成：同一导出内可复现、可审计；
// 不同导出（不同密钥）噪声不同。
type LaplaceNoise struct {
	Scale float64
}

func (LaplaceNoise) Name() string { return "laplace-noise" }

func (n LaplaceNoise) Apply(v interface{}, ctx *Context) interface{} {
	f, ok := toFloat(v)
	if !ok {
		return Deny{}.Apply(v, ctx)
	}
	u := ctx.rng.Float64() - 0.5
	noise := -n.Scale * sign(u) * math.Log(1-2*math.Abs(u))
	return math.Round((f+noise)*100) / 100
}

func sign(x float64) float64 {
	if x < 0 {
		return -1
	}
	return 1
}

func toFloat(v interface{}) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case float32:
		return float64(t), true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	}
	return 0, false
}

// seedFromKey 由导出密钥派生确定性随机种子。
func seedFromKey(key []byte) int64 {
	sum := sha256.Sum256(key)
	return int64(binary.BigEndian.Uint64(sum[:8]))
}
