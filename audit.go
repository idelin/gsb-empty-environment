package netsec

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Reason 说明整条日志消息被拒绝入库的原因类别。
type Reason string

const (
	ReasonJNDI           Reason = "jndi-reference"
	ReasonUnknownPrefix  Reason = "unknown-prefix"
	ReasonUnbalanced     Reason = "unbalanced-braces"
	ReasonDepthExceeded  Reason = "depth-exceeded"
	ReasonLengthExceeded Reason = "length-exceeded"
)

// RejectError 表示消息在查找展开审查中被判定为危险而应拒绝。
type RejectError struct {
	Reason Reason
	Detail string
}

func (e *RejectError) Error() string {
	return fmt.Sprintf("log message rejected (%s): %s", e.Reason, e.Detail)
}

// AsReject 从错误链中提取 RejectError。
func AsReject(err error) (*RejectError, bool) {
	var re *RejectError
	if errors.As(err, &re) {
		return re, true
	}
	return nil, false
}

// LookupFunc 解析一个已展开参数并返回替换值；ok 为 false 时回退到 :- 默认值。
// 实现必须是纯本地的（假值注入），不得发起任何网络解析。
type LookupFunc func(arg string) (value string, ok bool)

// Config 控制审查行为。Lookups 即调用方白名单，未列出的前缀一律拒绝。
type Config struct {
	Lookups   map[string]LookupFunc
	MaxDepth  int // ${...} 最大嵌套深度，<=0 用 DefaultMaxDepth
	MaxLength int // 展开中消息允许的最大长度，<=0 用 DefaultMaxLength
}

const (
	DefaultMaxDepth  = 8
	DefaultMaxLength = 4096
)

// AuditMessage 对入库前的日志消息执行由内向外逐层 ${前缀:参数:-默认值} 展开审查。
// 通过时返回完全展开后的消息；任何拒绝条件触发时返回 *RejectError。
func AuditMessage(msg string, cfg Config) (string, error) {
	maxDepth := cfg.MaxDepth
	if maxDepth <= 0 {
		maxDepth = DefaultMaxDepth
	}
	maxLen := cfg.MaxLength
	if maxLen <= 0 {
		maxLen = DefaultMaxLength
	}

	if len(msg) > maxLen {
		return "", &RejectError{ReasonLengthExceeded, fmt.Sprintf("initial length %d exceeds budget %d", len(msg), maxLen)}
	}
	if containsJNDI(msg) {
		return "", &RejectError{ReasonJNDI, "raw message contains jndi reference"}
	}

	for {
		start, end, rerr := innermost(msg, maxDepth)
		if rerr != nil {
			return "", rerr
		}
		if start < 0 {
			return msg, nil
		}
		replacement, rerr := evaluate(msg[start+2:end], cfg.Lookups)
		if rerr != nil {
			return "", rerr
		}
		msg = msg[:start] + replacement + msg[end+1:]
		if len(msg) > maxLen {
			return "", &RejectError{ReasonLengthExceeded, fmt.Sprintf("expanded length exceeds budget %d", maxLen)}
		}
		if containsJNDI(msg) {
			return "", &RejectError{ReasonJNDI, "expansion produced jndi reference"}
		}
	}
}

// innermost 定位第一个可求值的最内层 ${...}（其内部不再含 ${ 或 }）。
// 返回的 start/end 分别为 "${" 与配对 "}" 的下标；无表达式时 start 为 -1。
func innermost(msg string, maxDepth int) (start, end int, rerr *RejectError) {
	var stack []int
	for i := 0; i < len(msg); i++ {
		switch {
		case msg[i] == '$' && i+1 < len(msg) && msg[i+1] == '{':
			stack = append(stack, i)
			if len(stack) > maxDepth {
				return -1, -1, &RejectError{ReasonDepthExceeded, fmt.Sprintf("nesting deeper than %d levels", maxDepth)}
			}
			i++
		case msg[i] == '}':
			if len(stack) == 0 {
				return -1, -1, &RejectError{ReasonUnbalanced, "unmatched closing brace"}
			}
			return stack[len(stack)-1], i, nil
		}
	}
	if len(stack) > 0 {
		return -1, -1, &RejectError{ReasonUnbalanced, "unmatched opening ${"}
	}
	return -1, -1, nil
}

// evaluate 求值一个最内层表达式体（不含 ${ 与 }）。
func evaluate(body string, lookups map[string]LookupFunc) (string, *RejectError) {
	prefix, rest := body, ""
	hasArg := false
	if idx := strings.IndexByte(body, ':'); idx >= 0 {
		prefix, rest, hasArg = body[:idx], body[idx+1:], true
	}

	fn, ok := lookups[prefix]
	if !ok {
		return "", &RejectError{ReasonUnknownPrefix, fmt.Sprintf("prefix %q is not in the caller allowlist", prefix)}
	}

	arg, def := rest, ""
	hasDef := false
	if hasArg {
		if idx := strings.Index(rest, ":-"); idx >= 0 {
			arg, def, hasDef = rest[:idx], rest[idx+2:], true
		}
	}

	if value, ok := fn(arg); ok {
		return value, nil
	}
	if hasDef {
		return def, nil
	}
	return "", nil
}

func containsJNDI(msg string) bool {
	return strings.Contains(strings.ToLower(msg), "jndi")
}

// LowerLookup 白名单内置：小写化参数。
func LowerLookup(arg string) (string, bool) { return strings.ToLower(arg), true }

// UpperLookup 白名单内置：大写化参数。
func UpperLookup(arg string) (string, bool) { return strings.ToUpper(arg), true }

// EnvLookup 用调用方注入的假环境表解析 env 前缀，绝不读取真实进程环境。
func EnvLookup(values map[string]string) LookupFunc {
	return func(arg string) (string, bool) {
		v, ok := values[arg]
		return v, ok
	}
}

// DateLookup 用调用方注入的固定时间解析 date 前缀；参数非空时作为 Go 布局。
func DateLookup(now time.Time) LookupFunc {
	return func(arg string) (string, bool) {
		if arg == "" {
			return now.Format(time.RFC3339), true
		}
		return now.Format(arg), true
	}
}

// DefaultLookups 返回含 lower、upper、env、date 的白名单，env/date 均使用注入假值。
func DefaultLookups(env map[string]string, now time.Time) map[string]LookupFunc {
	return map[string]LookupFunc{
		"lower": LowerLookup,
		"upper": UpperLookup,
		"env":   EnvLookup(env),
		"date":  DateLookup(now),
	}
}
