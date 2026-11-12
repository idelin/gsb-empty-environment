// Package callgate 实现供解释器扩展调用钩子使用的 PHP 函数调用门。
//
// 调用门把被调函数名（命名空间前缀、大小写、Class::method、调用方式）与
// 回调解析结果统一规范化后，再与 disable_functions 等价的禁用集合比较，
// 并跟踪 putenv 对 LD_PRELOAD/LD_LIBRARY_PATH 的设置，阻断借 mail 系列
// 函数触发的外部程序执行路径。Guard 是有状态且并发安全的。
package callgate

import (
	"strings"
	"sync"
)

// Kind 描述调用发生的方式。
type Kind string

const (
	// KindDirect 是普通直接调用，例如 system(...)。
	KindDirect Kind = "direct"
	// KindVariable 是可变函数调用，例如 $f(...)。
	KindVariable Kind = "variable"
	// KindCallUserFunc 是 call_user_func / call_user_func_array 入口。
	KindCallUserFunc Kind = "call_user_func"
	// KindArrayMap 是 array_map / array_filter 等带回调参数的入口。
	KindArrayMap Kind = "array_map"
)

// CallbackKind 描述扩展对回调参数的解析结果类型。
type CallbackKind string

const (
	// CallbackString 是字符串回调，例如 "system" 或 "Class::method"。
	CallbackString CallbackKind = "string"
	// CallbackArray 是数组回调，例如 [Class, "method"]。
	CallbackArray CallbackKind = "array"
	// CallbackClosure 是闭包，没有可匹配的字符串名字。
	CallbackClosure CallbackKind = "closure"
	// CallbackInvocable 是实现 __invoke 的对象，没有可匹配的字符串名字。
	CallbackInvocable CallbackKind = "invocable"
)

// Callback 是扩展解析回调参数后得到的结构化结果。
type Callback struct {
	Kind   CallbackKind
	Name   string // Kind 为 CallbackString 时的完整回调字符串
	Class  string // Kind 为 CallbackArray 时的类名，仅记录，不参与匹配
	Method string // Kind 为 CallbackArray 时的方法名
}

// Call 是一次函数调用进入调用门时的全部输入。
type Call struct {
	Function string    // 被调函数名，允许大小写、前导反斜杠、命名空间与 Class::method
	Kind     Kind      // 调用方式
	Callback *Callback // 回调型调用的解析结果，普通调用为 nil
	Args     []string  // 字符串化后的参数，putenv 等需要检查参数内容
}

// Decision 是调用门的判定结果。
type Decision struct {
	Allowed bool
	Reason  string // 被拒绝时的原因码，放行时为空
	Matched string // 命中规则的规范化函数或方法名，放行时为空
}

// 判定原因码。
const (
	// ReasonDisabledFunction 表示直接或可变函数调用命中禁用集合。
	ReasonDisabledFunction = "disabled_function"
	// ReasonDisabledCallback 表示回调解析结果命中禁用集合。
	ReasonDisabledCallback = "disabled_callback"
	// ReasonTaintedMailer 表示 putenv 设置加载器变量后又调用 mail 系列函数。
	ReasonTaintedMailer = "tainted_mailer"
)

// defaultDisabled 是 disable_functions 展开后的外部程序执行函数集合。
var defaultDisabled = map[string]struct{}{
	"system":     {},
	"exec":       {},
	"passthru":   {},
	"shell_exec": {},
	"popen":      {},
	"proc_open":  {},
	"pcntl_exec": {},
}

// mailerFunctions 是会在 putenv 污染后被拒绝的 mail 系列函数。
var mailerFunctions = map[string]struct{}{
	"mail":         {},
	"mb_send_mail": {},
	"imap_open":    {},
}

// loaderVariables 是动态链接器相关的环境变量，putenv 触碰即产生污染。
var loaderVariables = map[string]struct{}{
	"LD_PRELOAD":      {},
	"LD_LIBRARY_PATH": {},
}

// Guard 是有状态、并发安全的调用门：除禁用集合匹配外，还跟踪
// putenv 对 LD_PRELOAD/LD_LIBRARY_PATH 的设置。污染采用 fail-closed
// 策略，一旦置位在 Guard 生命周期内不会清除。
type Guard struct {
	mu       sync.Mutex
	disabled map[string]struct{}
	tainted  bool
}

// NewGuard 创建带有默认禁用集合的调用门。
func NewGuard() *Guard {
	disabled := make(map[string]struct{}, len(defaultDisabled))
	for name := range defaultDisabled {
		disabled[name] = struct{}{}
	}
	return &Guard{disabled: disabled}
}

// NormalizeName 把函数名规范化为禁用集合使用的小写短名：
// 去掉命名空间前缀与前导反斜杠；Class::method 只保留方法名；统一小写。
func NormalizeName(name string) string {
	name = strings.TrimSpace(name)
	if i := strings.LastIndex(name, "::"); i >= 0 {
		name = name[i+len("::"):]
	} else if i := strings.LastIndexByte(name, '\\'); i >= 0 {
		name = name[i+1:]
	}
	name = strings.TrimSuffix(name, "()")
	return strings.ToLower(strings.TrimSpace(name))
}

// IsDisabled 以无状态方式判断名字规范化后是否命中禁用集合。
func IsDisabled(name string) bool {
	_, ok := defaultDisabled[NormalizeName(name)]
	return ok
}

// Check 对一次调用做判定并返回结果。调用方式（直接、可变函数、
// call_user_func、array_map）不影响匹配，全部走同一套规范化与比较逻辑。
func (g *Guard) Check(call Call) Decision {
	g.mu.Lock()
	defer g.mu.Unlock()

	if NormalizeName(call.Function) == "putenv" && setsLoaderVariable(call.Args) {
		g.tainted = true
	}

	names := []string{NormalizeName(call.Function)}
	resolved := ""
	if call.Callback != nil {
		resolved = resolveCallbackName(call.Callback)
		if resolved != "" {
			names = append(names, NormalizeName(resolved))
		}
	}

	for _, name := range names {
		if _, ok := g.disabled[name]; ok {
			reason := ReasonDisabledFunction
			if resolved != "" {
				reason = ReasonDisabledCallback
			}
			return Decision{Allowed: false, Reason: reason, Matched: name}
		}
	}

	if g.tainted {
		for _, name := range names {
			if _, ok := mailerFunctions[name]; ok {
				return Decision{Allowed: false, Reason: ReasonTaintedMailer, Matched: name}
			}
		}
	}

	return Decision{Allowed: true}
}

// Tainted 报告是否已观察到对加载器变量的 putenv 设置。
func (g *Guard) Tainted() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.tainted
}

// resolveCallbackName 取回调中需要匹配的名字：字符串回调返回完整字符串
// （Class::method 交由 NormalizeName 只留方法名），数组回调只返回方法名；
// 闭包与 __invoke 对象没有字符串名字，返回空串。
func resolveCallbackName(cb *Callback) string {
	switch cb.Kind {
	case CallbackString:
		return cb.Name
	case CallbackArray:
		return cb.Method
	default:
		return ""
	}
}

// setsLoaderVariable 判断 putenv 的首个参数是否设置或删除 LD_PRELOAD /
// LD_LIBRARY_PATH。变量名大小写不敏感，赋值（NAME=value）与删除
// （NAME）形式都视为触碰，配合粘性污染实现 fail-closed。
func setsLoaderVariable(args []string) bool {
	if len(args) == 0 {
		return false
	}
	key := strings.TrimSpace(args[0])
	if i := strings.IndexByte(key, '='); i >= 0 {
		key = key[:i]
	}
	key = strings.ToUpper(strings.TrimSpace(key))
	_, ok := loaderVariables[key]
	return ok
}
