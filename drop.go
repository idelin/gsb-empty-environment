package netsec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// 拖放目标组件的安全错误。调用方使用 errors.Is 判定具体原因。
var (
	// ErrInvalidPath 表示请求路径为空、绝对路径、卷路径或包含非法片段。
	ErrInvalidPath = errors.New("drop: invalid relative path")
	// ErrOutsideRoot 表示规范化或符号链接解析后的路径逃逸了会话目标目录。
	ErrOutsideRoot = errors.New("drop: path resolves outside the target directory")
	// ErrExecutableDenied 表示文件类型属于策略禁止的可执行类型。
	ErrExecutableDenied = errors.New("drop: executable file type is denied by policy")
	// ErrTooLarge 表示传入内容超过策略允许的最大字节数。
	ErrTooLarge = errors.New("drop: file exceeds the maximum allowed size")
	// ErrEmptyContent 表示实际写入的内容为空。
	ErrEmptyContent = errors.New("drop: empty file content")
)

// DefaultDeniedExtensions 是默认拒绝的可执行类型（小写、含点号）。
// 会话可通过 Policy.DeniedExtensions 覆盖；显式提供空切片表示不按扩展名拦截。
var DefaultDeniedExtensions = []string{
	".exe", ".dll", ".so", ".dylib", ".bin", ".bat", ".cmd",
	".com", ".scr", ".msi", ".ps1", ".vbs", ".js", ".jar",
	".app", ".deb", ".rpm", ".apk", ".sh",
}

// Policy 是会话级拖放策略。目标目录在策略中固定，组件不会写入该目录之外。
type Policy struct {
	// Root 是本会话唯一允许写入的目标目录，必须是绝对路径。
	Root string
	// MaxFileSize 限制单个文件的字节数，必须大于 0。
	MaxFileSize int64
	// DeniedExtensions 列出禁止接收的扩展名（大小写不敏感，需含点号）。
	// nil 时使用 DefaultDeniedExtensions；传入非 nil 的空切片表示不拦截扩展名。
	DeniedExtensions []string
}

// Target 是供远程桌面服务调用的拖放目标。零值不可用，必须通过 NewTarget 构造。
type Target struct {
	policy     Policy
	root       string // EvalSymlinks 后的绝对根目录
	deniedExts map[string]struct{}
}

// NewTarget 根据会话策略构造拖放目标，并立即解析根目录、复核其为真实目录。
func NewTarget(policy Policy) (*Target, error) {
	if policy.Root == "" {
		return nil, errors.New("drop: policy root is required")
	}
	if !filepath.IsAbs(policy.Root) {
		return nil, fmt.Errorf("drop: policy root must be an absolute path: %q", policy.Root)
	}
	if policy.MaxFileSize <= 0 {
		return nil, errors.New("drop: policy MaxFileSize must be positive")
	}

	cleanRoot := filepath.Clean(policy.Root)
	resolvedRoot, err := filepath.EvalSymlinks(cleanRoot)
	if err != nil {
		return nil, fmt.Errorf("drop: resolve target directory: %w", err)
	}
	info, err := os.Stat(resolvedRoot)
	if err != nil {
		return nil, fmt.Errorf("drop: stat target directory: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("drop: target is not a directory: %q", resolvedRoot)
	}

	target := &Target{
		policy: policy,
		root:   resolvedRoot,
	}
	if policy.DeniedExtensions == nil {
		target.deniedExts = normalizeExtensions(DefaultDeniedExtensions)
	} else {
		target.deniedExts = normalizeExtensions(policy.DeniedExtensions)
	}
	return target, nil
}

// Root 返回符号链接解析后的真实目标目录。
func (t *Target) Root() string {
	return t.root
}

// ReceiveFile 接收一次拖放文件写入。
//
// relPath 是客户端提供的相对路径，允许包含子目录（自动创建）。处理顺序：
//  1. 词法规范化，拒绝绝对路径、卷路径与 ".." 逃逸；
//  2. 逐段解析符号链接，任何解析结果逃逸根目录即拒绝；
//  3. 校验可执行扩展名与内容大小；
//  4. 在根目录内的临时目录写临时文件，落盘后以 rename(2) 原子替换目标；
//  5. 替换后再次 EvalSymlinks 复核最终路径仍在根目录内。
//
// 返回相对于根目录、使用本地分隔符的规范化相对路径。
func (t *Target) ReceiveFile(ctx context.Context, relPath string, content io.Reader) (string, error) {
	destDir, base, rel, err := t.secureJoin(relPath)
	if err != nil {
		return "", err
	}
	if err := t.checkType(base); err != nil {
		return "", err
	}

	tmpDir, err := t.stagingDir()
	if err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(tmpDir, ".drop-*")
	if err != nil {
		return "", fmt.Errorf("drop: create staging file: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpName) }

	n, err := copyLimited(ctx, tmp, content, t.policy.MaxFileSize)
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		cleanup()
		return "", err
	}
	if n == 0 {
		cleanup()
		return "", ErrEmptyContent
	}
	// 临时文件可能被外部干预为符号链接，替换前再次确认它仍是根目录内的普通文件。
	if err := t.verifyStaged(tmpName); err != nil {
		cleanup()
		return "", err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		cleanup()
		return "", fmt.Errorf("drop: finalize staging file: %w", err)
	}

	dest := filepath.Join(destDir, base)
	if err := os.Rename(tmpName, dest); err != nil {
		cleanup()
		return "", fmt.Errorf("drop: atomic replace: %w", err)
	}
	// 替换后复核：最终真实路径必须仍位于根目录之内。
	if err := t.verifyWithin(dest); err != nil {
		_ = os.Remove(dest)
		return "", err
	}
	return rel, nil
}

// Rename 在目标目录内原子重命名（移动）已接收的文件或目录，用于会话端重命名/整理。
// oldRel 与 newRel 都必须是根目录内的相对路径，newRel 的扩展名同样受策略约束。
func (t *Target) Rename(oldRel, newRel string) error {
	oldPath, err := t.resolveExisting(oldRel)
	if err != nil {
		return err
	}
	newDir, base, _, err := t.secureJoin(newRel)
	if err != nil {
		return err
	}
	if err := t.checkType(base); err != nil {
		return err
	}
	newPath := filepath.Join(newDir, base)

	if err := os.Rename(oldPath, newPath); err != nil {
		return fmt.Errorf("drop: atomic rename: %w", err)
	}
	// 替换后复核；失败时尝试回滚，避免把对象留在根目录之外。
	if err := t.verifyWithin(newPath); err != nil {
		_ = os.Rename(newPath, oldPath)
		return err
	}
	return nil
}

// secureJoin 把客户端相对路径安全地映射到根目录内，自动创建不存在的中间目录。
// 返回父目录绝对路径、文件名片段以及相对根目录的规范化路径。
func (t *Target) secureJoin(relPath string) (dir, base, rel string, err error) {
	segments, err := cleanSegments(relPath)
	if err != nil {
		return "", "", "", err
	}
	base = segments[len(segments)-1]
	rel = filepath.Join(segments...)

	current := t.root
	for _, segment := range segments[:len(segments)-1] {
		next := filepath.Join(current, segment)
		info, statErr := os.Lstat(next)
		switch {
		case statErr == nil:
			if info.Mode()&os.ModeSymlink != 0 {
				resolved, rerr := filepath.EvalSymlinks(next)
				if rerr != nil {
					return "", "", "", fmt.Errorf("drop: resolve symlink %q: %w", segment, rerr)
				}
				if !t.withinRoot(resolved) {
					return "", "", "", fmt.Errorf("%w: %s escapes via symlink", ErrOutsideRoot, relPath)
				}
				next = resolved
			} else if !info.IsDir() {
				return "", "", "", fmt.Errorf("drop: path component %q is not a directory", segment)
			}
		case errors.Is(statErr, fs.ErrNotExist):
			if mkErr := os.Mkdir(next, 0o755); mkErr != nil {
				return "", "", "", fmt.Errorf("drop: create directory %q: %w", segment, mkErr)
			}
		default:
			return "", "", "", fmt.Errorf("drop: inspect path component %q: %w", segment, statErr)
		}
		current = next
	}

	// 末级片段若已存在且为符号链接，解析后同样必须落在根目录内。
	final := filepath.Join(current, base)
	if info, statErr := os.Lstat(final); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
		resolved, rerr := filepath.EvalSymlinks(final)
		if rerr != nil {
			return "", "", "", fmt.Errorf("drop: resolve symlink %q: %w", base, rerr)
		}
		if !t.withinRoot(resolved) {
			return "", "", "", fmt.Errorf("%w: %s escapes via symlink", ErrOutsideRoot, relPath)
		}
	} else if statErr != nil && !errors.Is(statErr, fs.ErrNotExist) {
		return "", "", "", fmt.Errorf("drop: inspect destination: %w", statErr)
	}
	return current, base, rel, nil
}

// resolveExisting 解析一个必须已经存在的根目录内相对路径，逐段复核符号链接。
func (t *Target) resolveExisting(relPath string) (string, error) {
	segments, err := cleanSegments(relPath)
	if err != nil {
		return "", err
	}
	current := t.root
	for _, segment := range segments {
		next := filepath.Join(current, segment)
		resolved, err := filepath.EvalSymlinks(next)
		if err != nil {
			return "", fmt.Errorf("drop: resolve %q: %w", segment, err)
		}
		if !t.withinRoot(resolved) {
			return "", fmt.Errorf("%w: %s escapes via symlink", ErrOutsideRoot, relPath)
		}
		current = resolved
	}
	return current, nil
}

// stagingDir 返回根目录下专属的临时目录，保证所有临时文件都在受控目录内创建。
func (t *Target) stagingDir() (string, error) {
	dir := filepath.Join(t.root, ".drop-staging")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("drop: prepare staging directory: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", fmt.Errorf("drop: resolve staging directory: %w", err)
	}
	if !t.withinRoot(resolved) {
		return "", ErrOutsideRoot
	}
	return resolved, nil
}

// verifyStaged 确认临时文件是根目录内的普通文件（非符号链接、非设备）。
func (t *Target) verifyStaged(name string) error {
	info, err := os.Lstat(name)
	if err != nil {
		return fmt.Errorf("drop: inspect staging file: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("%w: staging file is not a regular file", ErrOutsideRoot)
	}
	return t.verifyWithin(name)
}

// verifyWithin 在全部符号链接解析后复核路径仍位于根目录之内。
func (t *Target) verifyWithin(path string) error {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return fmt.Errorf("drop: resolve final path: %w", err)
	}
	if !t.withinRoot(resolved) {
		return fmt.Errorf("%w: %s", ErrOutsideRoot, resolved)
	}
	return nil
}

// withinRoot 判断已解析的绝对路径是否等于根目录或位于其下。
func (t *Target) withinRoot(resolvedAbs string) bool {
	if resolvedAbs == t.root {
		return true
	}
	prefix := t.root + string(os.PathSeparator)
	return strings.HasPrefix(resolvedAbs, prefix)
}

// checkType 按策略拒绝可执行类型，扩展名比较大小写不敏感。
func (t *Target) checkType(base string) error {
	ext := strings.ToLower(filepath.Ext(base))
	if ext == "" {
		return nil
	}
	if _, denied := t.deniedExts[ext]; denied {
		return fmt.Errorf("%w: %s", ErrExecutableDenied, ext)
	}
	return nil
}

// cleanSegments 对客户端路径做词法规范化并拆分为安全片段。
func cleanSegments(relPath string) ([]string, error) {
	if relPath == "" {
		return nil, ErrInvalidPath
	}
	if strings.ContainsRune(relPath, 0) {
		return nil, fmt.Errorf("%w: NUL byte in path", ErrInvalidPath)
	}
	if filepath.IsAbs(relPath) || isVolumePath(relPath) {
		return nil, fmt.Errorf("%w: absolute paths are not allowed: %q", ErrInvalidPath, relPath)
	}
	cleaned := filepath.Clean(relPath)
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(os.PathSeparator)) {
		return nil, fmt.Errorf("%w: traversal outside target: %q", ErrInvalidPath, relPath)
	}
	var segments []string
	for _, segment := range strings.Split(cleaned, string(os.PathSeparator)) {
		switch segment {
		case "", ".":
			continue
		case "..":
			return nil, fmt.Errorf("%w: traversal outside target: %q", ErrInvalidPath, relPath)
		}
		segments = append(segments, segment)
	}
	if len(segments) == 0 {
		return nil, fmt.Errorf("%w: path has no file name: %q", ErrInvalidPath, relPath)
	}
	return segments, nil
}

// isVolumePath 识别 Windows 卷路径（如 C:\）与 UNC 路径，避免跨平台客户端绕过。
func isVolumePath(path string) bool {
	if len(path) >= 2 && path[1] == ':' {
		letter := path[0]
		if (letter >= 'a' && letter <= 'z') || (letter >= 'A' && letter <= 'Z') {
			return true
		}
	}
	return strings.HasPrefix(path, `\\`) || strings.HasPrefix(path, "//")
}

// normalizeExtensions 把策略扩展名统一为小写、含点号的集合。
func normalizeExtensions(exts []string) map[string]struct{} {
	out := make(map[string]struct{}, len(exts))
	for _, ext := range exts {
		ext = strings.TrimSpace(ext)
		if ext == "" {
			continue
		}
		if !strings.HasPrefix(ext, ".") {
			ext = "." + ext
		}
		out[strings.ToLower(ext)] = struct{}{}
	}
	return out
}

// copyLimited 拷贝最多 limit 字节，超出即返回 ErrTooLarge；同时响应 ctx 取消。
func copyLimited(ctx context.Context, dst io.Writer, src io.Reader, limit int64) (int64, error) {
	written, err := io.Copy(dst, &limitedReader{ctx: ctx, r: src, limit: limit + 1})
	if err != nil {
		return written, err
	}
	if written > limit {
		return written, ErrTooLarge
	}
	return written, nil
}

// limitedReader 在读取阶段即拦截超限内容，避免恶意上传撑大磁盘。
type limitedReader struct {
	ctx   context.Context
	r     io.Reader
	limit int64
}

func (l *limitedReader) Read(p []byte) (int, error) {
	if err := l.ctx.Err(); err != nil {
		return 0, err
	}
	if l.limit <= 0 {
		return 0, ErrTooLarge
	}
	if int64(len(p)) > l.limit {
		p = p[:l.limit]
	}
	n, err := l.r.Read(p)
	l.limit -= int64(n)
	return n, err
}
