// Package hostpath 为 Kubernetes 准入控制器提供 hostPath 卷与 volumeMount 的
// 宿主路径安全判定。
//
// 判定基于调用方提供的白名单前缀、禁止清单与宿主符号链接表，不访问真实文件系统：
//   - 路径必须为绝对路径，先去除重复斜杠与 "." 段（折叠前结果），再对 ".." 做
//     词法折叠（折叠后结果），两个结果都必须落在白名单前缀内；
//   - 白名单前缀按路径段边界比较，/var/log 不会覆盖 /var/log2；
//   - 符号链接表中的链接逐段展开，展开结果必须仍在白名单内，且不得命中禁止清单；
//   - 折叠或展开后命中禁止清单（根目录、/proc、/sys、/etc、/var/lib/kubelet、
//     形如 *.sock 的套接字）一律拒绝，即使位于白名单前缀之下；
//   - hostPath.type 为空时不做存在性检查，仍执行路径判定；
//   - subPath/subPathExpr 含 ".." 段、绝对路径或展开后越出卷目录一律拒绝；
//   - readOnly 为 false 的挂载只允许落在标注可写的白名单前缀之下。
package hostpath

import (
	"errors"
	"fmt"
	"strings"
)

// 判定失败的哨兵错误，调用方可用 errors.Is 判别具体原因。
var (
	// ErrNotAbsolute 表示路径为空、相对或包含非法字符。
	ErrNotAbsolute = errors.New("hostpath: path must be an absolute clean path")
	// ErrWhitelist 表示折叠前、折叠后或符号链接展开后的路径不在白名单内。
	ErrWhitelist = errors.New("hostpath: path is outside the allowed prefix whitelist")
	// ErrDenied 表示路径命中禁止清单（含 *.sock 套接字）。
	ErrDenied = errors.New("hostpath: path matches the deny list")
	// ErrSymlinkLoop 表示符号链接展开过程中检测到环。
	ErrSymlinkLoop = errors.New("hostpath: symlink loop detected")
	// ErrUnknownType 表示 hostPath.type 不是 Kubernetes 支持的取值。
	ErrUnknownType = errors.New("hostpath: unknown hostPath type")
	// ErrSubPathAbsolute 表示 subPath/subPathExpr 展开后为绝对路径。
	ErrSubPathAbsolute = errors.New("hostpath: subPath must not be an absolute path")
	// ErrSubPathParent 表示 subPath/subPathExpr 含 ".." 段。
	ErrSubPathParent = errors.New("hostpath: subPath must not contain a parent reference (..)")
	// ErrSubPathEscape 表示 subPath 经符号链接展开后越出卷目录。
	ErrSubPathEscape = errors.New("hostpath: subPath resolves outside the volume directory")
	// ErrSubPathVar 表示 subPathExpr 引用了环境变量表中不存在的变量。
	ErrSubPathVar = errors.New("hostpath: subPathExpr references an undefined variable")
	// ErrSubPathConflict 表示 subPath 与 subPathExpr 同时被设置。
	ErrSubPathConflict = errors.New("hostpath: subPath and subPathExpr must not be set together")
	// ErrWritable 表示读写挂载落在未标注可写的白名单前缀之下。
	ErrWritable = errors.New("hostpath: read-write mount requires a writable allowed prefix")
)

// Prefix 是一条白名单前缀，Writable 标注该前缀是否允许读写挂载。
type Prefix struct {
	Path     string
	Writable bool
}

// HostPathVolume 对应 Kubernetes v1.HostPathVolumeSource 的判定输入。
type HostPathVolume struct {
	// Path 是宿主路径，必须为绝对路径。
	Path string
	// Type 是宿主类型，取值为空或 Kubernetes HostPathType 之一；
	// 为空时不做存在性检查，仍按路径判定。
	Type string
}

// VolumeMount 对应容器中与 hostPath 卷关联的 volumeMount 判定输入。
type VolumeMount struct {
	Name        string
	SubPath     string
	SubPathExpr string
	// ReadOnly 为 false 时，挂载目标必须位于可写白名单前缀之下。
	ReadOnly bool
}

// SymlinkTable 是宿主文件系统的符号链接表：键为规范化后的符号链接绝对路径，
// 值为链接目标（可为绝对路径或相对路径）。表中不存在的路径视为普通文件/目录。
type SymlinkTable map[string]string

// DenyList 是调用方提供的禁止清单。
type DenyList struct {
	// Paths 为禁止目录/文件的绝对路径，目录条目同时禁止其下所有后代；
	// 根目录 "/" 仅禁止根本身。
	Paths []string
	// DenySockets 为 true 时，基名匹配 *.sock 的路径一律拒绝。
	DenySockets bool
}

// DefaultDenyList 返回题目要求的默认禁止清单：
// 根目录、/proc、/sys、/etc、/var/lib/kubelet 以及 *.sock 套接字。
func DefaultDenyList() *DenyList {
	return &DenyList{
		Paths:       []string{"/", "/proc", "/sys", "/etc", "/var/lib/kubelet"},
		DenySockets: true,
	}
}

// allowedPrefix 是内部规范化后的白名单条目。
type allowedPrefix struct {
	path     string
	writable bool
}

// Validator 持有一次准入策略所需的白名单、禁止清单与符号链接表，
// 可并发用于多个卷/挂载的判定（构造后只读）。
type Validator struct {
	prefixes []allowedPrefix
	deny     DenyList
	links    map[string]string
}

// New 构造 Validator。deny 为 nil 时使用 DefaultDenyList；白名单与禁止清单中的
// 路径会做词法规范化，符号链接表的键同样规范化，值在展开时按需规范化。
func New(allowed []Prefix, deny *DenyList, links SymlinkTable) *Validator {
	if deny == nil {
		deny = DefaultDenyList()
	}

	v := &Validator{deny: DenyList{DenySockets: deny.DenySockets}}
	for _, p := range allowed {
		v.prefixes = append(v.prefixes, allowedPrefix{
			path:     fold(p.Path),
			writable: p.Writable,
		})
	}
	for _, p := range deny.Paths {
		v.deny.Paths = append(v.deny.Paths, fold(p))
	}

	v.links = make(map[string]string, len(links))
	for link, target := range links {
		v.links[fold(link)] = target
	}
	return v
}

// 已知的 Kubernetes HostPathType 取值；空字符串表示不做存在性检查。
var knownHostPathTypes = map[string]bool{
	"":                  true,
	"File":              true,
	"Directory":         true,
	"FileOrCreate":      true,
	"DirectoryOrCreate": true,
	"Socket":            true,
	"CharDevice":        true,
	"BlockDevice":       true,
}

// ValidateHostPath 判定单个 hostPath 卷本身是否允许挂载。
func (v *Validator) ValidateHostPath(vol HostPathVolume) error {
	if !knownHostPathTypes[vol.Type] {
		return fmt.Errorf("%w: %q", ErrUnknownType, vol.Type)
	}
	return v.CheckPath(vol.Path)
}

// CheckPath 对一条绝对宿主路径执行完整判定：
// 绝对路径校验、折叠前/折叠后白名单、禁止清单、符号链接展开后白名单与禁止清单。
func (v *Validator) CheckPath(path string) error {
	if !isAbsolute(path) {
		return fmt.Errorf("%w: %q", ErrNotAbsolute, path)
	}

	before := preFold(path)
	after := fold(path)

	if !v.allowed(before) {
		return fmt.Errorf("%w: pre-fold path %q", ErrWhitelist, before)
	}
	if !v.allowed(after) {
		return fmt.Errorf("%w: folded path %q", ErrWhitelist, after)
	}
	if v.denied(after) {
		return fmt.Errorf("%w: folded path %q", ErrDenied, after)
	}

	expanded, err := v.expand(after)
	if err != nil {
		return err
	}
	if !v.allowed(expanded) {
		return fmt.Errorf("%w: symlink-expanded path %q", ErrWhitelist, expanded)
	}
	if v.denied(expanded) {
		return fmt.Errorf("%w: symlink-expanded path %q", ErrDenied, expanded)
	}
	return nil
}

// ValidateMount 判定容器的一个 volumeMount：先校验 hostPath 卷，再校验
// subPath/subPathExpr 与读写约束。env 用于展开 subPathExpr 中的 $(VAR_NAME)。
func (v *Validator) ValidateMount(vol HostPathVolume, mount VolumeMount, env map[string]string) error {
	if err := v.ValidateHostPath(vol); err != nil {
		return err
	}

	subPath := mount.SubPath
	if mount.SubPathExpr != "" {
		if mount.SubPath != "" {
			return ErrSubPathConflict
		}
		expandedExpr, err := expandSubPathExpr(mount.SubPathExpr, env)
		if err != nil {
			return err
		}
		subPath = expandedExpr
	}

	if subPath != "" {
		if strings.HasPrefix(subPath, "/") {
			return fmt.Errorf("%w: %q", ErrSubPathAbsolute, subPath)
		}
		for _, seg := range segments(subPath) {
			if seg == ".." {
				return fmt.Errorf("%w: %q", ErrSubPathParent, subPath)
			}
		}
	}

	foldedVolume := fold(vol.Path)
	expandedVolume, err := v.expand(foldedVolume)
	if err != nil {
		return err
	}

	effective := expandedVolume
	if subPath != "" {
		joined := joinPath(foldedVolume, subPath)
		effective, err = v.expand(joined)
		if err != nil {
			return err
		}
		if !hasPathPrefix(effective, expandedVolume) {
			return fmt.Errorf("%w: %q escapes %q", ErrSubPathEscape, effective, expandedVolume)
		}
	}

	if !v.allowed(effective) {
		return fmt.Errorf("%w: mount target %q", ErrWhitelist, effective)
	}
	if v.denied(effective) {
		return fmt.Errorf("%w: mount target %q", ErrDenied, effective)
	}

	if !mount.ReadOnly {
		if !v.writable(foldedVolume) || !v.writable(effective) {
			return fmt.Errorf("%w: mount target %q", ErrWritable, effective)
		}
	}
	return nil
}

// allowed 报告路径是否被任意白名单前缀按段边界覆盖。
func (v *Validator) allowed(path string) bool {
	for _, p := range v.prefixes {
		if hasPathPrefix(path, p.path) {
			return true
		}
	}
	return false
}

// writable 报告路径是否被某个标注可写的白名单前缀覆盖。
func (v *Validator) writable(path string) bool {
	for _, p := range v.prefixes {
		if p.writable && hasPathPrefix(path, p.path) {
			return true
		}
	}
	return false
}

// denied 报告折叠或展开后的路径是否命中禁止清单。
func (v *Validator) denied(path string) bool {
	for _, deniedPath := range v.deny.Paths {
		if deniedPath == "/" {
			if path == "/" {
				return true
			}
			continue
		}
		if hasPathPrefix(path, deniedPath) {
			return true
		}
	}
	if v.deny.DenySockets && strings.HasSuffix(baseName(path), ".sock") {
		return true
	}
	return false
}

// expand 按段展开符号链接。seen 用于检测同一条解析链上的链接环。
func (v *Validator) expand(path string) (string, error) {
	return v.walkLinks(segments(path), map[string]struct{}{})
}

func (v *Validator) walkLinks(segs []string, seen map[string]struct{}) (string, error) {
	cur := "/"
	for idx, seg := range segs {
		cur = joinPath(cur, seg)
		target, isLink := v.links[cur]
		if !isLink {
			continue
		}
		if _, repeated := seen[cur]; repeated {
			return "", fmt.Errorf("%w at %q", ErrSymlinkLoop, cur)
		}
		seen[cur] = struct{}{}

		if !strings.HasPrefix(target, "/") {
			target = fold(joinPath(dirName(cur), target))
		} else {
			target = fold(target)
		}

		head, err := v.walkLinks(segments(target), seen)
		if err != nil {
			return "", err
		}
		rest := segs[idx+1:]
		if len(rest) == 0 {
			return head, nil
		}
		merged := make([]string, 0, len(segments(head))+len(rest))
		merged = append(merged, segments(head)...)
		merged = append(merged, rest...)
		return v.walkLinks(merged, seen)
	}
	return cur, nil
}

// expandSubPathExpr 按 Kubernetes 规则展开 $(VAR_NAME) 形式的变量引用。
func expandSubPathExpr(expr string, env map[string]string) (string, error) {
	var b strings.Builder
	for i := 0; i < len(expr); {
		if i+1 < len(expr) && expr[i] == '$' && expr[i+1] == '(' {
			closeAt := strings.IndexByte(expr[i+2:], ')')
			if closeAt < 0 {
				return "", fmt.Errorf("%w: malformed expression %q", ErrSubPathVar, expr)
			}
			name := expr[i+2 : i+2+closeAt]
			value, ok := env[name]
			if !ok {
				return "", fmt.Errorf("%w: $(%s)", ErrSubPathVar, name)
			}
			b.WriteString(value)
			i = i + 2 + closeAt + 1
			continue
		}
		b.WriteByte(expr[i])
		i++
	}
	return b.String(), nil
}

// isAbsolute 校验路径非空、以 "/" 开头且不含 NUL 字节。
func isAbsolute(path string) bool {
	if path == "" || !strings.HasPrefix(path, "/") {
		return false
	}
	return !strings.ContainsRune(path, 0)
}

// segments 拆分路径的有效段（忽略空段与 "." 段）。
func segments(path string) []string {
	out := make([]string, 0, strings.Count(path, "/"))
	for _, seg := range strings.Split(path, "/") {
		if seg == "" || seg == "." {
			continue
		}
		out = append(out, seg)
	}
	return out
}

// preFold 仅去除重复斜杠与 "." 段，保留 ".." 段，得到折叠前结果。
func preFold(path string) string {
	segs := segments(path)
	if len(segs) == 0 {
		return "/"
	}
	return "/" + strings.Join(segs, "/")
}

// fold 在 preFold 基础上对 ".." 做词法折叠，等价于 filepath.Clean 的词法行为，
// 但不依赖运行平台的分隔符规则。
func fold(path string) string {
	stack := make([]string, 0, strings.Count(path, "/"))
	for _, seg := range segments(path) {
		switch seg {
		case "..":
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			// 位于根之上的 ".." 折叠为根，直接丢弃。
		default:
			stack = append(stack, seg)
		}
	}
	if len(stack) == 0 {
		return "/"
	}
	return "/" + strings.Join(stack, "/")
}

// joinPath 拼接父目录与相对段并做词法折叠。
func joinPath(parent, child string) string {
	return fold(parent + "/" + child)
}

// hasPathPrefix 按路径段边界判断 path 是否等于 prefix 或位于 prefix 之下。
// 例如 /var/log2 不被前缀 /var/log 覆盖。
func hasPathPrefix(path, prefix string) bool {
	pathSegs := segments(path)
	prefixSegs := segments(prefix)
	if len(pathSegs) < len(prefixSegs) {
		return false
	}
	for i, seg := range prefixSegs {
		if pathSegs[i] != seg {
			return false
		}
	}
	return true
}

// baseName 返回路径的最后一个段，根路径返回 "/"。
func baseName(path string) string {
	segs := segments(path)
	if len(segs) == 0 {
		return "/"
	}
	return segs[len(segs)-1]
}

// dirName 返回路径的父目录。
func dirName(path string) string {
	segs := segments(path)
	if len(segs) <= 1 {
		return "/"
	}
	return "/" + strings.Join(segs[:len(segs)-1], "/")
}
