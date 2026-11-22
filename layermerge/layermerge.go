// Package layermerge 实现镜像解包器的层合并组件。
//
// 组件按顺序应用 OCI/Docker 风格的 tar 层，职责包括：
//   - 按层顺序应用 OCI 白化文件（.wh. 前缀与 .wh..wh..opq 不透明目录标记）；
//   - 校验每层摘要（压缩后的原始 blob）与声明摘要一致；
//   - 校验压缩格式与层 MediaType 一致；
//   - 拒绝跨层路径逃逸（tar 条目名、符号链接、硬链接均限制在根目录内）；
//   - 摘要或格式不符的层整体拒绝，且不会改动已经合并的根目录。
package layermerge

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/bzip2"
	"compress/gzip"
	"crypto"
	_ "crypto/sha256"
	_ "crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// OCI/Docker 常见层 MediaType。
const (
	MediaTypeLayerTar    = "application/vnd.oci.image.layer.v1.tar"
	MediaTypeLayerGzip   = "application/vnd.oci.image.layer.v1.tar+gzip"
	MediaTypeLayerZstd   = "application/vnd.oci.image.layer.v1.tar+zstd"
	MediaTypeDockerTar   = "application/vnd.docker.image.rootfs.diff.tar"
	MediaTypeDockerGzip  = "application/vnd.docker.image.rootfs.diff.tar.gzip"
	MediaTypeDockerZstd  = "application/vnd.docker.image.rootfs.diff.tar.zstd"
	MediaTypeXGTar       = "application/x-tar"
	MediaTypeXGzip       = "application/gzip"
	MediaTypeXGzipLegacy = "application/x-gzip"
)

const (
	whiteoutPrefix    = ".wh."
	whiteoutOpaqueDir = ".wh..wh..opq"
)

// Layer 描述一个待合并的镜像层。
type Layer struct {
	// Digest 为压缩 blob 的摘要，形如 sha256:hex。
	Digest string
	// MediaType 声明层的压缩格式，空值按 Docker 历史默认 gzip 处理。
	MediaType string
	// Open 打开压缩后的层 blob；每次调用返回一个新的 reader。
	Open func() (io.ReadCloser, error)
}

// Options 控制合并过程的临时目录位置。
type Options struct {
	// WorkDir 用于存放每层的暂存目录，为空时在 rootDir 同级创建。
	WorkDir string
}

// ErrorKind 标识层失败的具体类别。
type ErrorKind string

const (
	ErrInvalidDigest  ErrorKind = "invalid digest"
	ErrDigestMismatch ErrorKind = "digest mismatch"
	ErrFormatMismatch ErrorKind = "compression format mismatch"
	ErrPathEscape     ErrorKind = "path escape"
	ErrUnsupported    ErrorKind = "unsupported feature"
	ErrLayerRead      ErrorKind = "layer read failed"
)

// LayerError 表示某一层（1 起编号）在合并过程中被拒绝。
type LayerError struct {
	// Index 为层号，从 1 开始。
	Index int
	// Kind 为失败类别。
	Kind ErrorKind
	// Op 为触发失败的操作阶段。
	Op string
	// Path 为相关的 tar 条目或链接路径（若适用）。
	Path string
	// Digest 为该层声明的摘要。
	Digest string
	// MediaType 为该层声明的 MediaType。
	MediaType string
	// Expected 与 Got 在摘要不符时分别给出期望与实际摘要。
	Expected string
	Got      string
	err      error
}

func (e *LayerError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "layer %d %s", e.Index, e.Kind)
	if e.Op != "" {
		fmt.Fprintf(&b, " during %s", e.Op)
	}
	if e.Path != "" {
		fmt.Fprintf(&b, ": path %q", e.Path)
	}
	switch e.Kind {
	case ErrDigestMismatch:
		fmt.Fprintf(&b, ": expected %s, got %s", e.Expected, e.Got)
	case ErrFormatMismatch:
		fmt.Fprintf(&b, ": mediaType %s", e.MediaType)
	}
	if e.err != nil {
		fmt.Fprintf(&b, ": %v", e.err)
	}
	return b.String()
}

func (e *LayerError) Unwrap() error { return e.err }

func layerError(index int, layer Layer, kind ErrorKind, op, path string, cause error) *LayerError {
	return &LayerError{
		Index:     index,
		Kind:      kind,
		Op:        op,
		Path:      path,
		Digest:    layer.Digest,
		MediaType: layer.MediaType,
		err:       cause,
	}
}

type compressionKind int

const (
	compressionUnknown compressionKind = iota
	compressionNone
	compressionGzip
	compressionZstd
	compressionBzip2
)

func (c compressionKind) String() string {
	switch c {
	case compressionNone:
		return "tar (uncompressed)"
	case compressionGzip:
		return "gzip"
	case compressionZstd:
		return "zstd"
	case compressionBzip2:
		return "bzip2"
	default:
		return "unknown"
	}
}

type digestSpec struct {
	algorithm string
	hash      crypto.Hash
	encoded   string
}

func parseDigest(ref string) (digestSpec, error) {
	algorithm, encoded, ok := strings.Cut(ref, ":")
	if !ok || algorithm == "" || encoded == "" {
		return digestSpec{}, fmt.Errorf("digest %q is not in algorithm:hex form", ref)
	}
	var ch crypto.Hash
	switch algorithm {
	case "sha256":
		ch = crypto.SHA256
	case "sha384":
		ch = crypto.SHA384
	case "sha512":
		ch = crypto.SHA512
	default:
		return digestSpec{}, fmt.Errorf("unsupported digest algorithm %q", algorithm)
	}
	if len(encoded) != ch.Size()*2 {
		return digestSpec{}, fmt.Errorf("digest %q has wrong length for %s", ref, algorithm)
	}
	if _, err := hex.DecodeString(encoded); err != nil {
		return digestSpec{}, fmt.Errorf("digest %q is not valid hex: %w", ref, err)
	}
	return digestSpec{algorithm: algorithm, hash: ch, encoded: encoded}, nil
}

func expectedCompression(mediaType string) (compressionKind, bool) {
	switch mediaType {
	case "", MediaTypeDockerGzip, MediaTypeLayerGzip, MediaTypeXGzip, MediaTypeXGzipLegacy:
		return compressionGzip, true
	case MediaTypeLayerTar, MediaTypeDockerTar, MediaTypeXGTar:
		return compressionNone, true
	case MediaTypeLayerZstd, MediaTypeDockerZstd:
		return compressionZstd, true
	default:
		switch {
		case strings.HasSuffix(mediaType, "+gzip"), strings.HasSuffix(mediaType, ".gzip"):
			return compressionGzip, true
		case strings.HasSuffix(mediaType, "+zstd"), strings.HasSuffix(mediaType, ".zstd"):
			return compressionZstd, true
		case strings.HasSuffix(mediaType, "+tar"), strings.HasSuffix(mediaType, ".tar"):
			return compressionNone, true
		default:
			return compressionUnknown, false
		}
	}
}

// detectCompression 通过魔数识别 blob 的压缩/归档格式。
func detectCompression(head []byte) compressionKind {
	switch {
	case len(head) >= 2 && head[0] == 0x1f && head[1] == 0x8b:
		return compressionGzip
	case len(head) >= 3 && head[0] == 'B' && head[1] == 'Z' && head[2] == 'h':
		return compressionBzip2
	case len(head) >= 4 && head[0] == 0x28 && head[1] == 0xb5 && head[2] == 0x2f && head[3] == 0xfd:
		return compressionZstd
	case len(head) >= 262 && bytes.Equal(head[257:262], []byte("ustar")):
		return compressionNone
	default:
		return compressionUnknown
	}
}

// MergeLayers 按切片顺序把各层合并到 rootDir。
//
// 任一层校验失败时返回 *LayerError，其中 Index 为 1 起的层号；失败层及其后
// 的层不会被应用，rootDir 保持上一个成功层合并后的状态。
func MergeLayers(rootDir string, layers []Layer, opts *Options) error {
	if rootDir == "" {
		return errors.New("rootDir must not be empty")
	}
	workDir := ""
	if opts != nil {
		workDir = opts.WorkDir
	}
	if workDir == "" {
		workDir = filepath.Join(filepath.Dir(rootDir), "."+filepath.Base(rootDir)+".staging")
	}
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return fmt.Errorf("create work directory: %w", err)
	}
	if err := os.MkdirAll(rootDir, 0o755); err != nil {
		return fmt.Errorf("create root directory: %w", err)
	}

	for i, layer := range layers {
		index := i + 1
		if err := mergeOne(index, layer, rootDir, workDir); err != nil {
			return err
		}
	}
	return nil
}

func mergeOne(index int, layer Layer, rootDir, workDir string) error {
	spec, err := parseDigest(layer.Digest)
	if err != nil {
		return layerError(index, layer, ErrInvalidDigest, "parse digest", "", err)
	}
	if layer.Open == nil {
		return layerError(index, layer, ErrLayerRead, "open blob", "", errors.New("layer has no Open func"))
	}

	stage, err := os.MkdirTemp(workDir, fmt.Sprintf("layer-%d-*", index))
	if err != nil {
		return layerError(index, layer, ErrLayerRead, "create staging dir", "", err)
	}
	defer os.RemoveAll(stage)

	stageRoot := filepath.Join(stage, "root")
	if err := os.Mkdir(stageRoot, 0o755); err != nil {
		return layerError(index, layer, ErrLayerRead, "create staging root", "", err)
	}

	blob, err := layer.Open()
	if err != nil {
		return layerError(index, layer, ErrLayerRead, "open blob", "", err)
	}

	hasher := spec.hash.New()
	source := io.TeeReader(blob, hasher)
	buffered := bufio.NewReader(source)

	head, peekErr := buffered.Peek(512)
	if peekErr != nil && peekErr != io.EOF && !errors.Is(peekErr, bufio.ErrBufferFull) {
		blob.Close()
		return layerError(index, layer, ErrLayerRead, "sniff compression", "", peekErr)
	}
	detected := detectCompression(head)
	expected, known := expectedCompression(layer.MediaType)
	if !known {
		blob.Close()
		return layerError(index, layer, ErrUnsupported, "match mediaType", "",
			fmt.Errorf("mediaType %q is not recognized", layer.MediaType))
	}
	if detected != expected {
		blob.Close()
		return layerError(index, layer, ErrFormatMismatch, "match mediaType", "",
			fmt.Errorf("expected %s per mediaType but blob is %s", expected, detected))
	}

	decompressed, err := newDecompressor(buffered, detected)
	if err != nil {
		blob.Close()
		return layerError(index, layer, ErrLayerRead, "decompress", "", err)
	}

	tarFile, err := os.Create(filepath.Join(stage, "layer.tar"))
	if err != nil {
		blob.Close()
		return layerError(index, layer, ErrLayerRead, "spool tar", "", err)
	}
	if _, err := io.Copy(tarFile, decompressed); err != nil {
		tarFile.Close()
		blob.Close()
		return layerError(index, layer, ErrLayerRead, "decompress", "", err)
	}
	if closer, ok := decompressed.(io.Closer); ok {
		_ = closer.Close()
	}
	if err := tarFile.Close(); err != nil {
		blob.Close()
		return layerError(index, layer, ErrLayerRead, "spool tar", "", err)
	}
	// 读完全部 blob 后摘要才完整，关闭以释放句柄。
	if err := blob.Close(); err != nil {
		return layerError(index, layer, ErrLayerRead, "close blob", "", err)
	}

	gotDigest := spec.algorithm + ":" + hex.EncodeToString(hasher.Sum(nil))
	if gotDigest != layer.Digest {
		le := layerError(index, layer, ErrDigestMismatch, "verify digest", "", nil)
		le.Expected = layer.Digest
		le.Got = gotDigest
		return le
	}

	whiteouts, err := extractTar(tarFile.Name(), stageRoot, index, layer)
	if err != nil {
		return err
	}
	if err := promoteLayer(stageRoot, rootDir, whiteouts); err != nil {
		return layerError(index, layer, ErrLayerRead, "promote layer", "", err)
	}
	return nil
}

func newDecompressor(r io.Reader, kind compressionKind) (io.Reader, error) {
	switch kind {
	case compressionNone:
		return r, nil
	case compressionGzip:
		gz, err := gzip.NewReader(r)
		if err != nil {
			return nil, fmt.Errorf("gzip reader: %w", err)
		}
		return gz, nil
	case compressionBzip2:
		return bzip2.NewReader(r), nil
	case compressionZstd:
		return nil, errors.New("zstd layers are not supported by this build")
	default:
		return nil, errors.New("unrecognized compression format")
	}
}

// safeJoin 把 tar 条目名锚定在根目录 root 内，并拒绝任何逃逸路径。
// 返回相对根目录的干净路径（使用系统分隔符）与绝对落地路径。
func safeJoin(root, name string) (rel, dest string, err error) {
	if err := rejectEscapeName(name); err != nil {
		return "", "", err
	}
	cleaned := filepath.Clean(string(os.PathSeparator) + strings.ReplaceAll(name, "\\", "/"))
	rel = filepath.Clean(strings.TrimPrefix(cleaned, string(os.PathSeparator)))
	if rel == "." {
		return "", root, nil
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return "", "", fmt.Errorf("entry %q escapes root", name)
	}
	dest = filepath.Join(root, rel)
	// 二次确认：解析后的落地路径必须仍在 root 之下。
	relCheck, err := filepath.Rel(root, dest)
	if err != nil || relCheck == ".." || strings.HasPrefix(relCheck, ".."+string(os.PathSeparator)) {
		return "", "", fmt.Errorf("entry %q escapes root", name)
	}
	return rel, dest, nil
}

// rejectEscapeName 在任何路径清洗之前检查原始 tar 名称/链接名，
// 拒绝绝对路径、反斜杠以及任何 ".." 路径组件。
func rejectEscapeName(name string) error {
	if name == "" {
		return errors.New("empty entry name")
	}
	if strings.ContainsRune(name, '\\') {
		return fmt.Errorf("entry %q contains backslash", name)
	}
	if filepath.IsAbs(name) || strings.HasPrefix(name, "/") {
		return fmt.Errorf("entry %q is absolute", name)
	}
	for _, part := range strings.Split(filepath.ToSlash(name), "/") {
		if part == ".." {
			return fmt.Errorf("entry %q escapes root", name)
		}
	}
	return nil
}

// linkInsideRoot 校验符号链接目标（可能是绝对或相对路径）不会指向 root 之外。
func linkInsideRoot(root, entryRel, target string) (string, error) {
	target = filepath.FromSlash(target)
	var resolved string
	if filepath.IsAbs(target) {
		// tar 中的绝对链接目标只能解释为根目录内的绝对路径。
		resolved = filepath.Join(root, target)
	} else {
		resolved = filepath.Join(root, filepath.Dir(entryRel), target)
	}
	resolved = filepath.Clean(resolved)
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("symlink target %q escapes root", target)
	}
	return resolved, nil
}

type whiteoutEntry struct {
	opaque bool
	// opaque 时为不透明目录的相对路径；普通白化时为被删除条目的相对路径。
	rel string
}

func extractTar(tarPath, root string, index int, layer Layer) ([]whiteoutEntry, error) {
	f, err := os.Open(tarPath)
	if err != nil {
		return nil, layerError(index, layer, ErrLayerRead, "open tar", "", err)
	}
	defer f.Close()

	tr := tar.NewReader(f)
	whiteouts := make([]whiteoutEntry, 0)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, layerError(index, layer, ErrLayerRead, "read tar", "", err)
		}
		// archive/tar 会静默清洗条目名中的 ".."，因此必须先对原始名称做逃逸检查。
		if err := rejectEscapeName(hdr.Name); err != nil {
			return nil, layerError(index, layer, ErrPathEscape, "extract", hdr.Name, err)
		}
		rel, dest, err := safeJoin(root, hdr.Name)
		if err != nil {
			return nil, layerError(index, layer, ErrPathEscape, "extract", hdr.Name, err)
		}
		if rel == "" {
			continue
		}

		base := filepath.Base(rel)
		if base == whiteoutOpaqueDir {
			whiteouts = append(whiteouts, whiteoutEntry{opaque: true, rel: filepath.Dir(rel)})
			continue
		}
		if strings.HasPrefix(base, whiteoutPrefix) {
			targetRel := filepath.Join(filepath.Dir(rel), strings.TrimPrefix(base, whiteoutPrefix))
			whiteouts = append(whiteouts, whiteoutEntry{rel: targetRel})
			continue
		}

		if err := extractEntry(tr, hdr, root, rel, dest, index, layer); err != nil {
			return nil, err
		}
	}
	return whiteouts, nil
}

func extractEntry(tr *tar.Reader, hdr *tar.Header, root, rel, dest string, index int, layer Layer) error {
	// 符号链接不落地文件，但必须先验证目标不逃逸。
	if hdr.Typeflag == tar.TypeSymlink {
		if _, err := linkInsideRoot(root, rel, hdr.Linkname); err != nil {
			return layerError(index, layer, ErrPathEscape, "extract", hdr.Name, err)
		}
	}
	// 硬链接目标必须是同层 tar 内、根目录之下的路径。
	if hdr.Typeflag == tar.TypeLink {
		if err := rejectEscapeName(hdr.Linkname); err != nil {
			return layerError(index, layer, ErrPathEscape, "extract", hdr.Name,
				fmt.Errorf("hardlink target %q escapes root", hdr.Linkname))
		}
		linkRel, linkDest, err := safeJoin(root, hdr.Linkname)
		if err != nil {
			return layerError(index, layer, ErrPathEscape, "extract", hdr.Name,
				fmt.Errorf("hardlink target %q escapes root", hdr.Linkname))
		}
		_ = linkRel
		if _, err := os.Lstat(linkDest); err != nil {
			return layerError(index, layer, ErrLayerRead, "extract", hdr.Name,
				fmt.Errorf("hardlink target %q not found in layer", hdr.Linkname))
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return layerError(index, layer, ErrLayerRead, "extract", hdr.Name, err)
		}
		_ = os.Remove(dest)
		if err := os.Link(linkDest, dest); err != nil {
			return layerError(index, layer, ErrLayerRead, "extract", hdr.Name, err)
		}
		_ = os.Chtimes(dest, hdr.AccessTime, hdr.ModTime)
		return nil
	}

	// 落地前确保父目录均为真实目录，避免沿既有符号链接写出根目录。
	if err := ensureParentDirs(root, dest); err != nil {
		return layerError(index, layer, ErrPathEscape, "extract", hdr.Name, err)
	}

	switch hdr.Typeflag {
	case tar.TypeDir:
		if err := os.MkdirAll(dest, os.FileMode(hdr.Mode)&0o777|0o700); err != nil {
			return layerError(index, layer, ErrLayerRead, "extract", hdr.Name, err)
		}
		if err := os.Chmod(dest, os.FileMode(hdr.Mode)&0o777); err != nil {
			return layerError(index, layer, ErrLayerRead, "extract", hdr.Name, err)
		}
	case tar.TypeReg, tar.TypeRegA:
		if err := writeRegular(dest, tr, os.FileMode(hdr.Mode)&0o777); err != nil {
			return layerError(index, layer, ErrLayerRead, "extract", hdr.Name, err)
		}
	case tar.TypeSymlink:
		_ = os.Remove(dest)
		if err := os.Symlink(filepath.FromSlash(hdr.Linkname), dest); err != nil {
			return layerError(index, layer, ErrLayerRead, "extract", hdr.Name, err)
		}
	case tar.TypeChar, tar.TypeBlock, tar.TypeFifo:
		// 无根设备权限时跳过设备节点与 FIFO，不影响普通文件层合并。
		return nil
	default:
		return layerError(index, layer, ErrUnsupported, "extract", hdr.Name,
			fmt.Errorf("tar typeflag %d is not supported", hdr.Typeflag))
	}
	return nil
}

func writeRegular(dest string, r io.Reader, mode os.FileMode) error {
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode|0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Chmod(dest, mode)
}

// ensureParentDirs 逐级创建父目录；若任一中间组件已是符号链接则拒绝，
// 防止通过 symlink 把后续条目写到根目录之外。
func ensureParentDirs(root, dest string) error {
	parent := filepath.Dir(dest)
	rel, err := filepath.Rel(root, parent)
	if err != nil {
		return err
	}
	if rel == "." {
		return nil
	}
	parts := strings.Split(rel, string(os.PathSeparator))
	cur := root
	for _, part := range parts {
		cur = filepath.Join(cur, part)
		info, err := os.Lstat(cur)
		if err != nil {
			if os.IsNotExist(err) {
				if err := os.Mkdir(cur, 0o755); err != nil {
					return err
				}
				continue
			}
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("path component %q is a symlink; refusing to traverse", cur)
		}
	}
	return nil
}

// promoteLayer 先应用本层的白化意图，再把暂存层合并进根目录。
// 全部操作均在本层通过摘要与格式校验之后才执行。
func promoteLayer(stageRoot, root string, whiteouts []whiteoutEntry) error {
	opaque := make([]string, 0)
	removed := make([]string, 0)
	for _, w := range whiteouts {
		if w.opaque {
			opaque = append(opaque, w.rel)
		} else {
			removed = append(removed, w.rel)
		}
	}
	// 先处理不透明目录：清空根目录中对应目录的既有内容。
	for _, rel := range opaque {
		if rel == "" || rel == "." {
			if err := clearDir(root); err != nil {
				return err
			}
			continue
		}
		target := filepath.Join(root, rel)
		info, err := os.Lstat(target)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		if info.IsDir() {
			if err := clearDir(target); err != nil {
				return err
			}
		}
	}
	// 再处理普通白化：删除根目录中被标记的既有条目。
	for _, rel := range removed {
		if rel == "" || rel == "." {
			continue
		}
		if err := os.RemoveAll(filepath.Join(root, rel)); err != nil {
			return err
		}
	}
	return moveMerge(stageRoot, root)
}

func clearDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(dir, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

// moveMerge 自底向上把 src 合并进 dst：同路径目录递归合并，
// 其余类型（文件、符号链接等）整体替换。优先 rename，跨设备时回退为复制。
func moveMerge(src, dst string) error {
	srcInfo, err := os.Lstat(src)
	if err != nil {
		return err
	}
	dstInfo, err := os.Lstat(dst)
	if err != nil {
		if os.IsNotExist(err) {
			return moveEntry(src, dst, srcInfo)
		}
		return err
	}
	if srcInfo.IsDir() && dstInfo.IsDir() {
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := moveMerge(filepath.Join(src, entry.Name()), filepath.Join(dst, entry.Name())); err != nil {
				return err
			}
		}
		return os.Remove(src)
	}
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	return moveEntry(src, dst, srcInfo)
}

func moveEntry(src, dst string, info os.FileInfo) error {
	err := os.Rename(src, dst)
	if err == nil {
		return nil
	}
	// EXDEV：暂存目录与根目录可能不在同一文件系统，回退为复制后删除。
	if copyErr := copyMove(src, dst, info); copyErr != nil {
		return copyErr
	}
	return os.RemoveAll(src)
}

func copyMove(src, dst string, info os.FileInfo) error {
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		return os.Symlink(target, dst)
	case info.IsDir():
		if err := os.MkdirAll(dst, info.Mode()&0o777); err != nil {
			return err
		}
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			childInfo, err := entry.Info()
			if err != nil {
				return err
			}
			if err := copyMove(filepath.Join(src, entry.Name()), filepath.Join(dst, entry.Name()), childInfo); err != nil {
				return err
			}
		}
		return os.Chmod(dst, info.Mode()&0o777)
	default:
		in, err := os.Open(src)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode()&0o777)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		if err := out.Close(); err != nil {
			return err
		}
		return os.Chmod(dst, info.Mode()&0o777)
	}
}
