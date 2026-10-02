package columnar

import (
	"encoding/json"
	"strings"
)

// action 决定扫描器在某一字段路径上的行为。
type action int

const (
	actSkip    action = iota // 词法跳过整棵子树，不物化任何值
	actCapture               // 物化该路径下的完整值（叶子或子树）
	actDescend               // 继续向下遍历，由子路径决定
)

// selector 根据“目标路径集合”判断每个路径的行为。
// matchAll 为 true 时退化为全量模式：所有容器下钻、所有标量捕获。
type selector struct {
	paths    []string
	matchAll bool
}

func newSelector(paths []string) *selector {
	cp := make([]string, len(paths))
	copy(cp, paths)
	return &selector{paths: cp}
}

func matchAllSelector() *selector { return &selector{matchAll: true} }

func (s *selector) action(p string) action {
	if s.matchAll || p == "" {
		return actDescend
	}
	for _, w := range s.paths {
		if w == p {
			return actCapture
		}
		// p 是某个目标路径的祖先：user / user.address 之于 user.address.city
		if strings.HasPrefix(w, p+".") || strings.HasPrefix(w, p+"[]") {
			return actDescend
		}
	}
	return actSkip
}

// emitScalar 决定标量叶子是否落列。
// 全量模式下所有标量都落列；投影模式下只有精确命中的路径落列
// （精确命中在 walk 入口已被 actCapture 处理，这里兜底）。
func (s *selector) emitScalar(p string) bool {
	if s.matchAll {
		return true
	}
	for _, w := range s.paths {
		if w == p {
			return true
		}
	}
	return false
}

// join 拼接字段路径：join("user","address") => "user.address"。
func join(parent, key string) string {
	if parent == "" {
		return key
	}
	return parent + "." + key
}

// walk 从 dec 消费一个 JSON 值，按 sel 决定捕获/下钻/跳过。
// 捕获到的值通过 emit(path, value) 回调输出。
// stats 非 nil 时累计跳过/捕获计数。
func walk(dec *json.Decoder, path string, sel *selector, emit func(string, any), stats *Stats) error {
	switch sel.action(path) {
	case actSkip:
		n, err := skipValue(dec)
		if stats != nil {
			stats.TokensSkipped += n
			if path != "" {
				stats.SkippedSubtrees = append(stats.SkippedSubtrees, path)
			}
		}
		return err
	case actCapture:
		v, err := decodeValue(dec)
		if err != nil {
			return err
		}
		if stats != nil {
			stats.ValuesCaptured++
		}
		emit(path, v)
		return nil
	}

	tok, err := dec.Token()
	if err != nil {
		return err
	}
	switch t := tok.(type) {
	case json.Delim:
		if t == '{' {
			for dec.More() {
				ktok, err := dec.Token()
				if err != nil {
					return err
				}
				if err := walk(dec, join(path, ktok.(string)), sel, emit, stats); err != nil {
					return err
				}
			}
			_, err := dec.Token() // 消费 '}'
			return err
		}
		// 数组：元素路径统一记为 parent+"[]"，对象数组与标量数组同等处理
		for dec.More() {
			if err := walk(dec, path+"[]", sel, emit, stats); err != nil {
				return err
			}
		}
		_, err := dec.Token() // 消费 ']'
		return err
	default:
		// 标量叶子
		if sel.emitScalar(path) {
			if stats != nil {
				stats.ValuesCaptured++
			}
			emit(path, t)
		} else if stats != nil {
			stats.TokensSkipped++
		}
		return nil
	}
}

// decodeValue 把流中的下一个 JSON 值完整物化为 Go 值。
// 仅在路径被精确选中时调用。
func decodeValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		if t == '{' {
			m := make(map[string]any)
			for dec.More() {
				ktok, err := dec.Token()
				if err != nil {
					return nil, err
				}
				v, err := decodeValue(dec)
				if err != nil {
					return nil, err
				}
				m[ktok.(string)] = v
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return m, nil
		}
		var arr []any
		for dec.More() {
			v, err := decodeValue(dec)
			if err != nil {
				return nil, err
			}
			arr = append(arr, v)
		}
		if _, err := dec.Token(); err != nil {
			return nil, err
		}
		return arr, nil
	default:
		return t, nil
	}
}

// skipValue 词法级跳过流中的下一个 JSON 值，返回消费的 token 数。
// 只推进解码器游标，不构建 map/slice，即“不反序列化”。
func skipValue(dec *json.Decoder) (int, error) {
	tok, err := dec.Token()
	if err != nil {
		return 0, err
	}
	n := 1
	if d, ok := tok.(json.Delim); ok {
		if d == '{' {
			for dec.More() {
				if _, err := dec.Token(); err != nil { // key
					return n, err
				}
				n++
				c, err := skipValue(dec)
				n += c
				if err != nil {
					return n, err
				}
			}
		} else {
			for dec.More() {
				c, err := skipValue(dec)
				n += c
				if err != nil {
					return n, err
				}
			}
		}
		if _, err := dec.Token(); err != nil { // 闭合分隔符
			return n, err
		}
		n++
	}
	return n, nil
}
