package telemetry

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ReservedPrefix 是系统保留的标签键前缀。溢出聚合桶使用保留键，
// 用户标签携带该前缀会被拒绝，因此溢出桶永远不会与真实同名标签冲突。
const ReservedPrefix = "__"

// OverflowLabelKey 是溢出聚合桶使用的保留标签键。
const OverflowLabelKey = ReservedPrefix + "overflow__"

// Label 是一个键值对标签。
type Label struct {
	Key   string
	Value string
}

var (
	errEmptyKey     = errors.New("telemetry: empty label key")
	errReservedKey  = errors.New("telemetry: label key uses reserved prefix " + ReservedPrefix)
	errDuplicateKey = errors.New("telemetry: duplicate label key")
)

// CanonicalLabels 返回排序后的标签副本。键顺序不同视为同一组合；
// 空键、保留前缀键、重复键都会被拒绝并返回错误。
func CanonicalLabels(labels []Label) ([]Label, error) {
	if len(labels) == 0 {
		return nil, nil
	}
	out := make([]Label, len(labels))
	copy(out, labels)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Key != out[j].Key {
			return out[i].Key < out[j].Key
		}
		return out[i].Value < out[j].Value
	})
	for i, l := range out {
		if l.Key == "" {
			return nil, errEmptyKey
		}
		if strings.HasPrefix(l.Key, ReservedPrefix) {
			return nil, fmt.Errorf("%w: %q", errReservedKey, l.Key)
		}
		if i > 0 && out[i-1].Key == l.Key {
			return nil, fmt.Errorf("%w: %q", errDuplicateKey, l.Key)
		}
	}
	return out, nil
}

// LabelKey 返回规范化标签集合的确定性字符串标识。
// 输入必须先经过 CanonicalLabels。
func LabelKey(canonical []Label) string {
	var b strings.Builder
	for i, l := range canonical {
		if i > 0 {
			b.WriteByte('\x1f')
		}
		b.WriteString(l.Key)
		b.WriteByte('=')
		b.WriteString(l.Value)
	}
	return b.String()
}
