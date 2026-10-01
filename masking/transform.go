// Package masking 提供声明式数据脱敏与合规导出内核。
//
// 支持的转换原语：
//   - MaskKeep：掩码，保留前 keepHead / 后 keepTail 位，中间以 maskChar 替换
//   - Pseudonym：哈希假名化，单次导出内使用同一随机密钥做 HMAC，
//     保证相同输入在单次导出中映射到相同假名（关联一致性）
//   - Noise：差分加噪（拉普拉斯噪声），用于数值字段的统计扰动
//   - RangeBucket：数值区间化，将精确值降级为区间范围
package masking

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	mathrand "math/rand"
	"strings"
)

// Transform 是对单个字段值执行脱敏转换的函数。
type Transform func(value any) any

// MaskKeep 返回一个掩码转换：保留前 head 位、后 tail 位，中间用 mask 字符替换。
// 例如手机号 13812345678 在 head=3, tail=4 下变为 138****5678。
func MaskKeep(head, tail int, mask rune) Transform {
	return func(value any) any {
		s, ok := value.(string)
		if !ok {
			return value
		}
		runes := []rune(s)
		if len(runes) <= head+tail {
			return strings.Repeat(string(mask), len(runes))
		}
		var b strings.Builder
		b.WriteString(string(runes[:head]))
		b.WriteString(strings.Repeat(string(mask), len(runes)-head-tail))
		b.WriteString(string(runes[len(runes)-tail:]))
		return b.String()
	}
}

// Pseudonymizer 在单次导出会话内提供一致的哈希假名化。
// 相同明文在同一 Pseudonymizer 下总是得到相同假名（关联一致性）；
// 不同导出会话使用不同随机密钥，假名不可跨导出关联。
type Pseudonymizer struct {
	key   []byte
	cache map[string]string
}

// NewPseudonymizer 创建带随机密钥的假名化器。
func NewPseudonymizer() (*Pseudonymizer, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("生成假名化密钥失败: %w", err)
	}
	return &Pseudonymizer{key: key, cache: make(map[string]string)}, nil
}

// Pseudonym 返回基于该假名化器的转换函数。
func (p *Pseudonymizer) Pseudonym(prefix string) Transform {
	return func(value any) any {
		s, ok := value.(string)
		if !ok {
			return value
		}
		if alias, hit := p.cache[s]; hit {
			return alias
		}
		mac := hmac.New(sha256.New, p.key)
		mac.Write([]byte(s))
		alias := prefix + hex.EncodeToString(mac.Sum(nil))[:12]
		p.cache[s] = alias
		return alias
	}
}

// LaplaceNoise 返回差分加噪转换：对数值叠加拉普拉斯噪声（scale = 敏感度/epsilon）。
// rnd 注入随机源以便测试可复现。
func LaplaceNoise(scale float64, rnd *mathrand.Rand) Transform {
	return func(value any) any {
		f, ok := toFloat(value)
		if !ok {
			return value
		}
		u := rnd.Float64() - 0.5
		noise := -scale * sign(u) * math.Log(1-2*math.Abs(u))
		return math.Round((f+noise)*100) / 100
	}
}

// RangeBucket 返回区间化转换：将数值向下归入宽度为 width 的区间，
// 输出 "低-高" 形式的字符串，例如薪资 23500 在 width=5000 下变为 "20000-25000"。
func RangeBucket(width float64) Transform {
	return func(value any) any {
		f, ok := toFloat(value)
		if !ok {
			return value
		}
		low := math.Floor(f/width) * width
		high := low + width
		return fmt.Sprintf("%d-%d", int64(low), int64(high))
	}
}

// Redact 返回固定替换转换（完全遮蔽）。
func Redact(replacement any) Transform {
	return func(any) any { return replacement }
}

func toFloat(value any) (float64, bool) {
	switch v := value.(type) {
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case float64:
		return v, true
	}
	return 0, false
}

func sign(x float64) float64 {
	if x < 0 {
		return -1
	}
	return 1
}
