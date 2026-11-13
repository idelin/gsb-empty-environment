package netsec

import "strings"

// URI 授权标志位（与 Android Intent 的 FLAG_GRANT_* 对应）。
const (
	FlagGrantReadURI   = 0x1  // 读授权
	FlagGrantWriteURI  = 0x2  // 写授权
	FlagGrantPrefixURI = 0x80 // 前缀授权

	grantFlagsMask = FlagGrantReadURI | FlagGrantWriteURI | FlagGrantPrefixURI

	// MaxNestedDepth 是 extras 中嵌套子意图允许的最大深度，
	// 被转发意图本身为第 0 层，超过该深度一律拒绝。
	MaxNestedDepth = 3
)

// Intent 描述一个待转发的意图。Extras 为嵌套的子意图，可继续嵌套。
type Intent struct {
	Flags    int
	Data     string
	ClipData []string
	Extras   []*Intent
}

// Decision 是判定结果。
type Decision int

const (
	// DecisionForward 表示可按原样转发。
	DecisionForward Decision = iota
	// DecisionStripFlags 表示必须清除授权标志（0x1/0x2/0x80）后才可转发。
	DecisionStripFlags
	// DecisionReject 表示必须拒绝转发（file 协议或嵌套超深）。
	DecisionReject
)

// Evaluate 判定 intent 是否可转发。whitelist 为调用方可授权的
// authority 白名单（大小写、是否带端口均不敏感）。
func Evaluate(intent *Intent, whitelist []string) Decision {
	if intent == nil {
		return DecisionForward
	}
	allowed := make(map[string]struct{}, len(whitelist))
	for _, authority := range whitelist {
		allowed[normalizeAuthority(authority)] = struct{}{}
	}
	res := inspect(intent, 0, allowed)
	switch {
	case res.reject:
		return DecisionReject
	case res.hasGrantFlags && !res.allURIsAllowed:
		return DecisionStripFlags
	default:
		return DecisionForward
	}
}

// StripGrantFlags 返回清除授权标志后的深拷贝，原意图不受影响；
// 拷贝中其余标志位保持不变。
func StripGrantFlags(intent *Intent) *Intent {
	if intent == nil {
		return nil
	}
	out := &Intent{
		Flags: intent.Flags &^ grantFlagsMask,
		Data:  intent.Data,
	}
	if intent.ClipData != nil {
		out.ClipData = append([]string(nil), intent.ClipData...)
	}
	if intent.Extras != nil {
		out.Extras = make([]*Intent, len(intent.Extras))
		for i, child := range intent.Extras {
			out.Extras[i] = StripGrantFlags(child)
		}
	}
	return out
}

type inspection struct {
	reject         bool
	hasGrantFlags  bool
	allURIsAllowed bool
}

func inspect(intent *Intent, depth int, allowed map[string]struct{}) inspection {
	res := inspection{allURIsAllowed: true}
	if intent == nil {
		return res
	}
	if depth > MaxNestedDepth {
		res.reject = true
		return res
	}
	if intent.Flags&grantFlagsMask != 0 {
		res.hasGrantFlags = true
	}
	uris := make([]string, 0, len(intent.ClipData)+1)
	if intent.Data != "" {
		uris = append(uris, intent.Data)
	}
	uris = append(uris, intent.ClipData...)
	for _, uri := range uris {
		scheme, authority := splitURI(uri)
		switch scheme {
		case "file":
			res.reject = true
			return res
		case "content":
			if _, ok := allowed[authority]; !ok {
				res.allURIsAllowed = false
			}
		}
	}
	for _, child := range intent.Extras {
		sub := inspect(child, depth+1, allowed)
		if sub.reject {
			res.reject = true
			return res
		}
		res.hasGrantFlags = res.hasGrantFlags || sub.hasGrantFlags
		res.allURIsAllowed = res.allURIsAllowed && sub.allURIsAllowed
	}
	return res
}

// splitURI 返回小写 scheme 与归一化后的 authority。
// scheme 取第一个 : 之前；authority 取 :// 之后、第一个 / 之前。
func splitURI(raw string) (scheme, authority string) {
	rest := raw
	if i := strings.IndexByte(rest, ':'); i >= 0 {
		scheme = strings.ToLower(rest[:i])
		rest = rest[i+1:]
	}
	if !strings.HasPrefix(rest, "//") {
		return scheme, ""
	}
	rest = rest[2:]
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		rest = rest[:i]
	}
	return scheme, normalizeAuthority(rest)
}

// normalizeAuthority 取最后一个 @ 之后的部分（用户信息不参与判定），
// 转小写并去掉端口。
func normalizeAuthority(authority string) string {
	if i := strings.LastIndexByte(authority, '@'); i >= 0 {
		authority = authority[i+1:]
	}
	authority = strings.ToLower(authority)
	if i := strings.LastIndexByte(authority, ':'); i >= 0 {
		if port := authority[i+1:]; isDigits(port) {
			authority = authority[:i]
		}
	}
	return authority
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
