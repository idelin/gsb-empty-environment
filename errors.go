package polyglot

import (
	"errors"
	"fmt"
	"strings"
)

// 哨兵错误，调用方可用 errors.Is 匹配。
var (
	// ErrAmbiguous 表示同一文件能被两种及以上格式成功解析。
	ErrAmbiguous = errors.New("polyglot: 内容可被多种格式解析，拒绝多义文件")
	// ErrUnknown 表示没有任何受支持格式能解析该内容。
	ErrUnknown = errors.New("polyglot: 无法解析为任何受支持的格式")
	// ErrExtensionMismatch 表示实际结构类型与调用方声明的扩展名不一致。
	ErrExtensionMismatch = errors.New("polyglot: 扩展名与实际内容结构不匹配")
	// ErrInvalidSize 表示传入的文件大小为负或超出 int 可表示范围。
	ErrInvalidSize = errors.New("polyglot: 无效的文件大小")
)

// AmbiguousError 描述一次多义文件命中，保证 errors.Is(err, ErrAmbiguous) 成立。
type AmbiguousError struct {
	// Formats 为全部解析成功的格式，按检测顺序排列且去重。
	Formats []Format
	// Reasons 记录每种格式解析成功时的结构定位说明。
	Reasons map[Format]string
}

func (e *AmbiguousError) Error() string {
	names := make([]string, len(e.Formats))
	for i, f := range e.Formats {
		names[i] = f.Extension()
	}
	return fmt.Sprintf("%s: %s", ErrAmbiguous, strings.Join(names, ", "))
}

func (e *AmbiguousError) Unwrap() error { return ErrAmbiguous }

// UnknownError 描述内容被全部格式拒绝的原因，保证 errors.Is(err, ErrUnknown) 成立。
type UnknownError struct {
	// Reasons 记录每种格式解析失败的具体原因。
	Reasons map[Format]string
}

func (e *UnknownError) Error() string {
	parts := make([]string, 0, len(e.Reasons))
	for _, f := range detectionOrder {
		reason, ok := e.Reasons[f]
		if !ok {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s: %s", f.Extension(), reason))
	}
	return fmt.Sprintf("%s (%s)", ErrUnknown, strings.Join(parts, "; "))
}

func (e *UnknownError) Unwrap() error { return ErrUnknown }

// ExtensionMismatchError 表示扩展名与结构判定结果不一致，
// 保证 errors.Is(err, ErrExtensionMismatch) 成立。
type ExtensionMismatchError struct {
	// Declared 为扩展名映射出的格式；无法识别时为零值 FormatUnknown。
	Declared Format
	// Detected 为按结构判定出的格式。
	Detected Format
}

func (e *ExtensionMismatchError) Error() string {
	return fmt.Sprintf("%s: 声明 %s，实际为 %s",
		ErrExtensionMismatch, e.Declared.Extension(), e.Detected.Extension())
}

func (e *ExtensionMismatchError) Unwrap() error { return ErrExtensionMismatch }
