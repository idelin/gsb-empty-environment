package taskpkg

import (
	"errors"
	"fmt"
	"math/big"
	"strings"
)

// groupChar 把分组规则映射为具体分组字节；GroupSpace 单独按 rune 处理。
func groupChar(grouping int) (byte, bool) {
	switch grouping {
	case GroupComma:
		return ',', true
	case GroupDot:
		return '.', true
	default:
		return 0, false
	}
}

func isSpaceGroup(r rune) bool {
	return r == ' ' || r == '\u00a0'
}

// ParseDecimal 按给定小数符、分组规则把原始字符串精确解析为有理数（不含单位倍率）。
//
// 空串（去除首尾空白后）表示缺失，返回 (nil, nil)。允许可选的前导正负号；
// 小数符至多出现一次；分组符不得出现在小数段，整数段除最左一组外每组必须恰好 3 位；
// 任何其他字符或畸形分组都会返回错误。GroupSpace 同时接受半角空格与 U+00A0。
func ParseDecimal(raw string, decimal byte, grouping int) (*big.Rat, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil, nil
	}
	if decimal != ',' && decimal != '.' {
		return nil, fmt.Errorf("taskpkg: 非法小数符 %q", decimal)
	}

	negative := false
	switch s[0] {
	case '+':
		s = s[1:]
	case '-':
		negative = true
		s = s[1:]
	}
	if s == "" {
		return nil, errors.New("taskpkg: 符号后缺少数字")
	}

	gByte, gIsByte := groupChar(grouping)
	if gIsByte && gByte == decimal {
		return nil, errors.New("taskpkg: 分组符与小数符相同")
	}

	// 切分整数段与小数段。
	intPart, fracPart := s, ""
	if idx := strings.IndexByte(s, decimal); idx >= 0 {
		if strings.IndexByte(s[idx+1:], decimal) >= 0 {
			return nil, errors.New("taskpkg: 小数符出现多次")
		}
		intPart, fracPart = s[:idx], s[idx+1:]
	}

	intDigits, err := validateGroups(intPart, grouping, gByte, gIsByte, true)
	if err != nil {
		return nil, err
	}
	fracDigits, err := validateGroups(fracPart, grouping, gByte, gIsByte, false)
	if err != nil {
		return nil, err
	}
	if intDigits == "" && fracDigits == "" {
		return nil, errors.New("taskpkg: 缺少数字")
	}
	if intDigits == "" {
		intDigits = "0"
	}

	unscaled := new(big.Int)
	if _, ok := unscaled.SetString(intDigits+fracDigits, 10); !ok {
		return nil, errors.New("taskpkg: 数字构造失败")
	}
	den := big.NewInt(1)
	if n := len(fracDigits); n > 0 {
		den.Exp(big.NewInt(10), big.NewInt(int64(n)), nil)
	}
	r := new(big.Rat).SetFrac(unscaled, den)
	if negative {
		r.Neg(r)
	}
	return r, nil
}

// validateGroups 校验一段（整数段或小数段）内的分组与数字。
// intSeg=true 时允许最左一组 1-3 位、其余每组恰 3 位；小数段不允许任何分组符，
// 且必须全部为数字。
func validateGroups(seg string, grouping int, gByte byte, gIsByte, intSeg bool) (string, error) {
	if seg == "" {
		return "", nil
	}
	if !intSeg || grouping == GroupNone {
		for _, r := range seg {
			if r < '0' || r > '9' {
				return "", fmt.Errorf("taskpkg: 非法字符 %q", r)
			}
		}
		return seg, nil
	}

	// 统一把空格分组符归一为同一个分隔字节便于处理。
	normalized := seg
	if grouping == GroupSpace {
		var b strings.Builder
		for _, r := range seg {
			if isSpaceGroup(r) {
				b.WriteByte(',')
			} else {
				b.WriteRune(r)
			}
		}
		normalized = b.String()
		gByte = ','
		gIsByte = true
	}

	parts := strings.Split(normalized, string(gByte))
	if len(parts) == 1 {
		for _, r := range parts[0] {
			if r < '0' || r > '9' {
				return "", fmt.Errorf("taskpkg: 非法字符 %q", r)
			}
		}
		if parts[0] == "" {
			return "", errors.New("taskpkg: 空数字段")
		}
		return parts[0], nil
	}
	for i, p := range parts {
		if p == "" {
			return "", errors.New("taskpkg: 存在空分组")
		}
		for _, r := range p {
			if r < '0' || r > '9' {
				return "", fmt.Errorf("taskpkg: 非法字符 %q", r)
			}
		}
		if i == 0 {
			if len(p) < 1 || len(p) > 3 {
				return "", errors.New("taskpkg: 整数段首组位数必须为 1-3 位")
			}
		} else if len(p) != 3 {
			return "", errors.New("taskpkg: 千分位分组每组必须恰为 3 位")
		}
	}
	return strings.Join(parts, ""), nil
}

// ProposeCandidates 依据单列样本枚举至多 4 个可解析全部非缺失单元的格式候选。
//
// 候选按固定优先级去重后截断：逗号分组+点小数、无分组+点小数、点分组+逗号小数、
// 无分组+逗号小数、空格分组+点小数、空格分组+逗号小数。单位倍率统一为 1，
// 宿主可在返回结果上自行替换 UnitScale 与 Name。
func ProposeCandidates(samples []string) ([]Candidate, error) {
	specs := []struct {
		name     string
		decimal  byte
		grouping int
	}{
		{"decimal-dot/group-comma", '.', GroupComma},
		{"decimal-dot/group-none", '.', GroupNone},
		{"decimal-comma/group-dot", ',', GroupDot},
		{"decimal-comma/group-none", ',', GroupNone},
		{"decimal-dot/group-space", '.', GroupSpace},
		{"decimal-comma/group-space", ',', GroupSpace},
	}
	var out []Candidate
	seen := map[string]bool{}
	for _, sp := range specs {
		if len(out) >= 4 {
			break
		}
		ok := true
		for _, raw := range samples {
			if _, err := ParseDecimal(raw, sp.decimal, sp.grouping); err != nil {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		key := fmt.Sprintf("%d/%d", sp.decimal, sp.grouping)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, Candidate{
			Name:     sp.name,
			Decimal:  sp.decimal,
			Grouping: sp.grouping,
		})
	}
	return out, nil
}

// ScaleRat 返回 r 乘以 scale 的新有理数；scale 为 nil 时按 1 处理。
func ScaleRat(r *big.Rat, scale *big.Rat) *big.Rat {
	if r == nil {
		return nil
	}
	if scale == nil {
		return new(big.Rat).Set(r)
	}
	return new(big.Rat).Mul(r, scale)
}
