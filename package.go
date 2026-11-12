package netsec

import (
	"errors"
	"strings"
	"sync"
)

var (
	// ErrExternalTarget 表示关系目标是外部绝对 URI，而调用方未允许外部目标。
	ErrExternalTarget = errors.New("opc: external relationship target is not allowed")
	// ErrExternalURI 表示外部目标本身不是合法的绝对 URI。
	ErrExternalURI = errors.New("opc: invalid external target URI")
	// ErrPathEscape 表示相对目标经过点段解析后越出包根。
	ErrPathEscape = errors.New("opc: relationship target escapes package root")
	// ErrMissingPart 表示解析得到的部件未登记在包中（HasPart 校验失败）。
	ErrMissingPart = errors.New("opc: relationship target part does not exist")
)

// Package 保存一个 OPC 包已登记的部件名，提供部件名去重与关系目标解析。
// 零值即可使用，且可被多个 goroutine 并发调用。
type Package struct {
	mu    sync.RWMutex
	parts map[string]string // 规范化键 -> 解码后的规范部件名
}

// NewPackage 创建空包，并用给定部件名初始化；任何部件名非法或重名都会返回错误。
func NewPackage(partNames ...string) (*Package, error) {
	pkg := &Package{parts: make(map[string]string)}
	for _, name := range partNames {
		if err := pkg.AddPart(name); err != nil {
			return nil, err
		}
	}
	return pkg, nil
}

// AddPart 校验并登记一个部件名。
//
// 名称必须符合 OPC 部件名规则；解码并忽略大小写后与已有部件重名时，
// 返回包装了 ErrDuplicatePart 的错误，原始集合保持不变。
func (pkg *Package) AddPart(name string) error {
	decoded, err := validatePartName(name)
	if err != nil {
		return err
	}
	key := canonicalKey(decoded)

	pkg.mu.Lock()
	defer pkg.mu.Unlock()
	if pkg.parts == nil {
		pkg.parts = make(map[string]string)
	}
	if _, exists := pkg.parts[key]; exists {
		return ErrDuplicatePart
	}
	pkg.parts[key] = decoded
	return nil
}

// HasPart 报告包中是否存在与 name 指向同一部件的条目（解码、忽略大小写）。
func (pkg *Package) HasPart(name string) bool {
	decoded, err := validatePartName(name)
	if err != nil {
		return false
	}
	pkg.mu.RLock()
	defer pkg.mu.RUnlock()
	_, ok := pkg.parts[canonicalKey(decoded)]
	return ok
}

// PartCount 返回已登记部件数量。
func (pkg *Package) PartCount() int {
	pkg.mu.RLock()
	defer pkg.mu.RUnlock()
	return len(pkg.parts)
}

// ResolveTarget 按 OPC 规则把关系 Target 解析为包内部件名。
//
// relsPart 是承载该关系的关系部件名（形如
// "/word/_rels/document.xml.rels" 或 "/_rels/.rels"）；target 是关系元素的
// Target 属性；targetMode 为 "External" 时目标是包外绝对 URI。
//
// 外部目标只有在 allowExternal 为 true 时原样保留，否则返回
// ErrExternalTarget。内部目标先按 RFC 3986 相对引用规则基于关系部件所在
// 目录合并，再做点段移除；任何越出包根的结果返回 ErrPathEscape。
// requirePart 为 true 时还会确认解析出的部件确实已登记在包中。
func (pkg *Package) ResolveTarget(relsPart, target, targetMode string, allowExternal, requirePart bool) (string, error) {
	if target == "" {
		return "", ErrInvalidPartName
	}

	mode := strings.TrimSpace(targetMode)
	if mode == "External" {
		if !allowExternal {
			return "", ErrExternalTarget
		}
		if !isAbsoluteURI(target) {
			return "", ErrExternalURI
		}
		return target, nil
	}
	if mode != "" && mode != "Internal" {
		return "", ErrInvalidPartName
	}

	// 内部目标不允许反斜线：OPC 路径分隔符只有 "/"，
	// 反斜线可能被下游文件 API 当作分隔符造成穿越。
	if strings.ContainsRune(target, '\\') {
		return "", ErrInvalidPartName
	}

	base, err := relationshipsBase(relsPart)
	if err != nil {
		return "", err
	}

	merged := mergePaths(base, target)

	cleaned, escaped := removeDotSegments(merged)
	if escaped {
		return "", ErrPathEscape
	}

	partName, err := validatePartName(cleaned)
	if err != nil {
		return "", err
	}

	if requirePart && !pkg.HasPart(partName) {
		return "", ErrMissingPart
	}
	return partName, nil
}

// relationshipsBase 返回关系部件的基准目录。
//
// 关系部件必须位于某个 "_rels" 目录中，目标相对其所在目录解析：
// "/word/_rels/document.xml.rels" -> "/word/"
// "/_rels/.rels"                  -> "/"
func relationshipsBase(relsPart string) (string, error) {
	decoded, err := validatePartName(relsPart)
	if err != nil {
		return "", err
	}
	idx := strings.LastIndex(decoded, "/_rels/")
	if idx < 0 {
		if decoded == "/_rels/.rels" {
			return "/", nil
		}
		return "", ErrInvalidPartName
	}
	return decoded[:idx+1], nil
}

// mergePaths 按 RFC 3986 第 5.3 节合并基准目录与相对引用。
// target 以 "/" 开头时为根相对引用，直接替换基准路径。
func mergePaths(baseDir, target string) string {
	if strings.HasPrefix(target, "/") {
		return target
	}
	return baseDir + target
}

// removeDotSegments 实现 RFC 3986 第 5.2.4 节的点段移除。
//
// 返回移除点段后的绝对路径；当 ".." 试图越过根目录时 escaped 为 true。
func removeDotSegments(path string) (string, bool) {
	var stack []string
	for _, segment := range strings.Split(path, "/") {
		switch segment {
		case "", ".":
			continue
		case "..":
			if len(stack) == 0 {
				return "", true
			}
			stack = stack[:len(stack)-1]
		default:
			stack = append(stack, segment)
		}
	}
	return "/" + strings.Join(stack, "/"), false
}

// isAbsoluteURI 仅放行带 scheme 且 scheme 合法的绝对 URI（RFC 3986）。
func isAbsoluteURI(target string) bool {
	schemeEnd := strings.IndexByte(target, ':')
	if schemeEnd < 1 {
		return false
	}
	scheme := target[:schemeEnd]
	for i := 0; i < len(scheme); i++ {
		ch := scheme[i]
		if i == 0 && !isAlpha(ch) {
			return false
		}
		if !isAlpha(ch) && !isDigit(ch) && ch != '+' && ch != '-' && ch != '.' {
			return false
		}
	}
	return true
}

func isAlpha(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

func isDigit(b byte) bool {
	return b >= '0' && b <= '9'
}
