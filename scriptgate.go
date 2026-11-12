package netsec

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// 脚本门禁相关错误。可用 errors.Is 判定具体类别。
var (
	// ErrDuplicateKey 表示查询体 JSON 中同一对象出现了重复键。
	ErrDuplicateKey = errors.New("netsec: duplicate JSON key")
	// ErrInlineScript 表示出现了 inline 脚本（字符串形式，或带
	// source/inline 字段）。
	ErrInlineScript = errors.New("netsec: inline script is not allowed")
	// ErrUnsafeScript 表示脚本对象不符合仅含 {id, params} 的存储脚本要求。
	ErrUnsafeScript = errors.New("netsec: only stored scripts of the form {\"id\", \"params\"} are allowed")
)

// scriptCarryingKeys 列出所有可以承载脚本的字段名，覆盖：
// script、script_score、runtime_mappings 字段脚本、scripted_metric 的
// 四个 *_script（init/map/combine/reduce）以及 bucket_script、bucket_selector。
var scriptCarryingKeys = map[string]bool{
	"script":          true,
	"script_score":    true,
	"init_script":     true,
	"map_script":      true,
	"combine_script":  true,
	"reduce_script":   true,
	"bucket_script":   true,
	"bucket_selector": true,
}

// ValidateQuery 对整个 ES 查询体做脚本门禁。
//
// 它递归检查任意深度上的 script、script_score、runtime_mappings 字段脚本、
// scripted_metric 的四个 *_script（init/map/combine/reduce）以及
// bucket_script、bucket_selector。只允许形如 {"id": "...", "params": {...}}
// 的存储脚本；字符串脚本、带 source/inline/lang 字段的脚本一律拒绝。
// JSON 中出现重复键同样拒绝。
func ValidateQuery(body []byte) error {
	if err := rejectDuplicateKeys(body); err != nil {
		return err
	}

	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var root any
	if err := dec.Decode(&root); err != nil {
		return fmt.Errorf("netsec: invalid JSON: %w", err)
	}
	if dec.More() {
		return fmt.Errorf("netsec: invalid JSON: %w", errTrailingData)
	}
	return validateNode(root, "$")
}

var errTrailingData = errors.New("multiple JSON values")

// ValidateQueryString 与 ValidateQuery 相同，但接受字符串入参，
// 便于直接对接网关的请求体读取结果。
func ValidateQueryString(body string) error {
	return ValidateQuery([]byte(body))
}

func validateNode(node any, path string) error {
	switch value := node.(type) {
	case map[string]any:
		for key, child := range value {
			childPath := path + "." + key
			if scriptCarryingKeys[key] {
				if err := validateScriptCarrier(child, childPath); err != nil {
					return err
				}
				continue
			}
			if err := validateNode(child, childPath); err != nil {
				return err
			}
		}
	case []any:
		for index, child := range value {
			if err := validateNode(child, fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
		}
	}
	return nil
}

// validateScriptCarrier 校验某个脚本承载字段的值，并继续检查其内部
// 嵌套的脚本（例如 script_score 包裹的 script）。
func validateScriptCarrier(node any, path string) error {
	switch value := node.(type) {
	case nil:
		return fmt.Errorf("%w at %s: null script", ErrUnsafeScript, path)
	case string:
		// 字符串形式即 inline 脚本（含已废止的 inline 写法）。
		return fmt.Errorf("%w at %s", ErrInlineScript, path)
	case map[string]any:
		// 包装形式（如 script_score: {query: ..., script: {...}}）：
		// 自身不是脚本对象，递归检查内部字段。
		if _, hasScript := value["script"]; hasScript {
			return validateNode(value, path)
		}
		return validateScriptSpec(value, path)
	default:
		return fmt.Errorf("%w at %s: unexpected script type %T", ErrUnsafeScript, path, node)
	}
}

// validateScriptSpec 校验脚本对象，仅放行 {"id": "...", "params": {...}}。
func validateScriptSpec(spec map[string]any, path string) error {
	if len(spec) == 0 {
		return fmt.Errorf("%w at %s: empty script object", ErrUnsafeScript, path)
	}
	for key := range spec {
		switch key {
		case "source", "inline":
			return fmt.Errorf("%w at %s: field %q", ErrInlineScript, path, key)
		case "lang":
			return fmt.Errorf("%w at %s: field %q", ErrUnsafeScript, path, key)
		case "id", "params":
		default:
			return fmt.Errorf("%w at %s: unexpected field %q", ErrUnsafeScript, path, key)
		}
	}

	id, hasID := spec["id"]
	if !hasID {
		return fmt.Errorf("%w at %s: missing stored-script id", ErrUnsafeScript, path)
	}
	idText, ok := id.(string)
	if !ok || idText == "" {
		return fmt.Errorf("%w at %s: id must be a non-empty string", ErrUnsafeScript, path)
	}

	// params 为不透明数据，不再向下展开，避免误伤业务参数。
	return nil
}

// frame 跟踪一个对象当前是否期待读取键。
type frame struct {
	isObject  bool
	expectKey bool
}

// rejectDuplicateKeys 用流式解码检测任意深度对象内的重复键。
func rejectDuplicateKeys(body []byte) error {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()

	var stack []frame
	seen := make([]map[string]bool, 0)

	for {
		token, err := dec.Token()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return fmt.Errorf("netsec: invalid JSON: %w", err)
		}

		if delimiter, ok := token.(json.Delim); ok {
			switch delimiter {
			case '{':
				stack = append(stack, frame{isObject: true, expectKey: true})
				seen = append(seen, make(map[string]bool))
			case '[':
				stack = append(stack, frame{isObject: false})
			case '}', ']':
				stack = stack[:len(stack)-1]
				if delimiter == '}' {
					seen = seen[:len(seen)-1]
				}
				// 闭合的容器本身是父对象的一个值，下一个 token 应为键。
				if len(stack) > 0 && stack[len(stack)-1].isObject {
					stack[len(stack)-1].expectKey = true
				}
			}
			continue
		}

		if len(stack) > 0 && stack[len(stack)-1].isObject && stack[len(stack)-1].expectKey {
			key, ok := token.(string)
			if !ok {
				return errors.New("netsec: invalid JSON: non-string object key")
			}
			depth := len(seen) - 1
			if seen[depth][key] {
				return fmt.Errorf("%w: %q", ErrDuplicateKey, key)
			}
			seen[depth][key] = true
			stack[len(stack)-1].expectKey = false
			continue
		}

		// 值之后若仍在对象中，下一个 token 应为键。
		if len(stack) > 0 && stack[len(stack)-1].isObject {
			stack[len(stack)-1].expectKey = true
		}
	}
	return nil
}
