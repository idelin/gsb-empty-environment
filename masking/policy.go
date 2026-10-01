package masking

import (
	"encoding/json"
	"fmt"
	mathrand "math/rand"
)

// RuleKind 声明式规则类型。
type RuleKind string

const (
	RuleMask      RuleKind = "mask"      // 掩码：保留前 N 后 M
	RulePseudonym RuleKind = "pseudonym" // 哈希假名化（单次导出内一致）
	RuleNoise     RuleKind = "noise"     // 差分加噪（拉普拉斯）
	RuleRange     RuleKind = "range"     // 数值区间化
	RuleRedact    RuleKind = "redact"    // 完全遮蔽
	RuleKeep      RuleKind = "keep"      // 明文保留
)

// FieldRule 声明单个字段在特定合规级别下的转换方式。
type FieldRule struct {
	Field string   `json:"field"`
	Rule  RuleKind `json:"rule"`
	// mask 参数
	KeepHead int    `json:"keep_head,omitempty"`
	KeepTail int    `json:"keep_tail,omitempty"`
	MaskChar string `json:"mask_char,omitempty"`
	// pseudonym 参数
	Prefix string `json:"prefix,omitempty"`
	// noise 参数
	NoiseScale float64 `json:"noise_scale,omitempty"`
	// range 参数
	BucketWidth float64 `json:"bucket_width,omitempty"`
	// redact 参数
	Replacement any `json:"replacement,omitempty"`
}

// RolePolicy 是某角色在特定合规级别下的完整字段规则集。
type RolePolicy struct {
	Role  string      `json:"role"`
	Level string      `json:"level"`
	Rules []FieldRule `json:"rules"`
}

// PolicySet 保存全部 (角色, 合规级别) -> 策略 的声明式配置，可整体从 JSON 加载。
type PolicySet struct {
	Policies []RolePolicy `json:"policies"`
}

// ParsePolicySet 从 JSON 解析声明式策略配置。
func ParsePolicySet(data []byte) (*PolicySet, error) {
	var ps PolicySet
	if err := json.Unmarshal(data, &ps); err != nil {
		return nil, fmt.Errorf("解析策略配置失败: %w", err)
	}
	return &ps, nil
}

// Lookup 查找指定角色与合规级别的策略。
func (ps *PolicySet) Lookup(role, level string) (*RolePolicy, error) {
	for i := range ps.Policies {
		p := &ps.Policies[i]
		if p.Role == role && p.Level == level {
			return p, nil
		}
	}
	return nil, fmt.Errorf("未找到角色 %q 在合规级别 %q 下的导出策略", role, level)
}

// compile 将声明式规则编译为可执行转换；假名化规则共享会话级 Pseudonymizer。
func (p *RolePolicy) compile(pseudo *Pseudonymizer, rnd *mathrand.Rand) (map[string]Transform, error) {
	compiled := make(map[string]Transform, len(p.Rules))
	for _, r := range p.Rules {
		t, err := r.compile(pseudo, rnd)
		if err != nil {
			return nil, err
		}
		compiled[r.Field] = t
	}
	return compiled, nil
}

func (r FieldRule) compile(pseudo *Pseudonymizer, rnd *mathrand.Rand) (Transform, error) {
	switch r.Rule {
	case RuleKeep:
		return func(v any) any { return v }, nil
	case RuleMask:
		mask := []rune(r.MaskChar)
		m := '*'
		if len(mask) > 0 {
			m = mask[0]
		}
		return MaskKeep(r.KeepHead, r.KeepTail, m), nil
	case RulePseudonym:
		prefix := r.Prefix
		if prefix == "" {
			prefix = "PSEUDO-"
		}
		return pseudo.Pseudonym(prefix), nil
	case RuleNoise:
		scale := r.NoiseScale
		if scale <= 0 {
			scale = 1
		}
		return LaplaceNoise(scale, rnd), nil
	case RuleRange:
		width := r.BucketWidth
		if width <= 0 {
			width = 1000
		}
		return RangeBucket(width), nil
	case RuleRedact:
		return Redact(r.Replacement), nil
	default:
		return nil, fmt.Errorf("字段 %q 使用了未知规则类型 %q", r.Field, r.Rule)
	}
}
