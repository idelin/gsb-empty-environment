package content

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
)

// Store 是基于文件系统的内容库。已提交的 blob 以摘要命名且只读；
// 进行中的写入会话保存在 ingest 目录下，以 ref 命名并持久保存已写偏移。
type Store struct {
	root string
}

// NewStore 创建以 root 为根目录的内容库。
func NewStore(root string) (*Store, error) {
	for _, dir := range []string{
		filepath.Join(root, "blobs", AlgorithmSHA256),
		filepath.Join(root, "blobs", AlgorithmSHA512),
		filepath.Join(root, "ingest"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	return &Store{root: root}, nil
}

// Status 描述写入会话的进度。
type Status struct {
	Ref      string
	Offset   int64
	Total    int64
	Expected Digest
}

// sessionMeta 是会话的持久化元数据。其中的偏移只用于定位续写位置，
// 摘要状态在恢复时总是从已写数据从头重算，绝不信任保存的摘要。
type sessionMeta struct {
	Ref      string `json:"ref"`
	Expected string `json:"expected"`
	Total    int64  `json:"total"`
	Offset   int64  `json:"offset"`
}

// refKey 把任意 ref 映射为安全的文件名，避免路径穿越。
func refKey(ref string) string {
	sum := sha256.Sum256([]byte(ref))
	return hex.EncodeToString(sum[:])
}

func (s *Store) blobPath(d Digest) string {
	return filepath.Join(s.root, "blobs", d.Algorithm(), d.Encoded())
}

func (s *Store) ingestPaths(ref string) (dataPath, metaPath string) {
	key := refKey(ref)
	return filepath.Join(s.root, "ingest", key+".tmp"),
		filepath.Join(s.root, "ingest", key+".json")
}

// Writer 是供拉取器使用的写入会话。
type Writer struct {
	store    *Store
	ref      string
	file     *os.File
	metaPath string
	dataPath string
	hash     hash.Hash
	offset   int64
	total    int64
	expected Digest
	closed   bool
}

// Writer 打开 ref 对应的写入会话。若该 ref 之前中断过，
// 会读取磁盘上的临时文件，从头重算已写数据的摘要状态后继续写入。
// expected 为声明的摘要，必须提供且只能使用受支持的算法。
func (s *Store) Writer(ref string, total int64, expected Digest) (*Writer, error) {
	if ref == "" {
		return nil, errors.New("content: ref must not be empty")
	}
	if expected == "" {
		return nil, errors.New("content: expected digest must not be empty")
	}
	if err := expected.Validate(); err != nil {
		return nil, err
	}
	dataPath, metaPath := s.ingestPaths(ref)

	meta, err := readMeta(metaPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("content: cannot read session metadata for ref %q: %w", ref, err)
	}

	var offset int64
	if meta != nil {
		if meta.Ref != ref {
			return nil, fmt.Errorf("content: session metadata ref %q does not match %q", meta.Ref, ref)
		}
		if meta.Expected != string(expected) {
			return nil, fmt.Errorf("content: ref %q resumed with a different expected digest", ref)
		}
		if total >= 0 && meta.Total >= 0 && meta.Total != total {
			return nil, fmt.Errorf("content: ref %q resumed with a different total size", ref)
		}
		offset = meta.Offset
	}

	file, err := os.OpenFile(dataPath, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}

	w := &Writer{
		store:    s,
		ref:      ref,
		file:     file,
		metaPath: metaPath,
		dataPath: dataPath,
		total:    total,
		expected: expected,
	}

	if offset > 0 {
		if err := w.rehash(offset); err != nil {
			file.Close()
			return nil, fmt.Errorf("content: cannot resume ref %q: %w", ref, err)
		}
	} else {
		h, err := expected.Hasher()
		if err != nil {
			file.Close()
			return nil, err
		}
		w.hash = h
		if err := w.truncate(0); err != nil {
			file.Close()
			return nil, err
		}
	}
	if err := w.persist(); err != nil {
		file.Close()
		return nil, err
	}
	return w, nil
}

// rehash 从头读取已写的 offset 字节重算摘要状态，
// 并截掉偏移之后可能存在的脏数据。
func (w *Writer) rehash(offset int64) error {
	info, err := w.file.Stat()
	if err != nil {
		return err
	}
	if info.Size() < offset {
		return fmt.Errorf("ingest file has %d bytes, fewer than the persisted offset %d", info.Size(), offset)
	}
	if info.Size() > offset {
		if err := w.file.Truncate(offset); err != nil {
			return err
		}
	}
	if _, err := w.file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	h, err := w.expected.Hasher()
	if err != nil {
		return err
	}
	if _, err := io.CopyN(h, w.file, offset); err != nil {
		return fmt.Errorf("failed to recompute digest state: %w", err)
	}
	w.hash = h
	w.offset = offset
	return nil
}

func (w *Writer) truncate(size int64) error {
	if err := w.file.Truncate(size); err != nil {
		return err
	}
	_, err := w.file.Seek(size, io.SeekStart)
	return err
}

// persist 把当前已写偏移保存到会话元数据中。
func (w *Writer) persist() error {
	meta := sessionMeta{
		Ref:      w.ref,
		Expected: string(w.expected),
		Total:    w.total,
		Offset:   w.offset,
	}
	buf, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	tmp := w.metaPath + ".tmp"
	if err := os.WriteFile(tmp, buf, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, w.metaPath)
}

func readMeta(path string) (*sessionMeta, error) {
	buf, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var meta sessionMeta
	if err := json.Unmarshal(buf, &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

// Status 返回会话当前的进度。
func (w *Writer) Status() Status {
	return Status{
		Ref:      w.ref,
		Offset:   w.offset,
		Total:    w.total,
		Expected: w.expected,
	}
}

// Digest 返回已写数据当前的摘要。
func (w *Writer) Digest() Digest {
	return Digest(fmt.Sprintf("%s:%x", w.expected.Algorithm(), w.hash.Sum(nil)))
}

// Write 追加数据到会话并持久保存新的已写偏移。
func (w *Writer) Write(p []byte) (int, error) {
	if w.closed {
		return 0, errors.New("content: write on closed writer")
	}
	n, err := w.file.Write(p)
	if err != nil {
		return n, err
	}
	if _, err := w.hash.Write(p[:n]); err != nil {
		return n, err
	}
	w.offset += int64(n)
	if err := w.persist(); err != nil {
		return n, err
	}
	return n, nil
}

// Commit 校验已写数据的摘要与大小，全部匹配才把内容固化为
// 以摘要命名的只读 blob；任一不符都会删除会话且不产生 blob。
// size 传负数表示不校验总大小，expected 传空表示沿用会话声明的摘要。
func (w *Writer) Commit(size int64, expected Digest) error {
	if w.closed {
		return errors.New("content: commit on closed writer")
	}
	if expected == "" {
		expected = w.expected
	}
	if err := expected.Validate(); err != nil {
		w.discard()
		return err
	}
	if expected != w.expected {
		w.discard()
		return fmt.Errorf("content: commit digest %s does not match session digest %s", expected, w.expected)
	}
	if actual := w.Digest(); actual != expected {
		w.discard()
		return fmt.Errorf("content: commit digest mismatch: got %s, expected %s", actual, expected)
	}
	if size >= 0 && size != w.offset {
		offset := w.offset
		w.discard()
		return fmt.Errorf("content: commit size mismatch: got %d, expected %d", offset, size)
	}
	if w.total >= 0 && w.total != w.offset {
		offset, total := w.offset, w.total
		w.discard()
		return fmt.Errorf("content: commit size mismatch: got %d, session declared %d", offset, total)
	}

	blobPath := w.store.blobPath(expected)
	if _, err := os.Stat(blobPath); err == nil {
		// 同摘要重复提交：只校验已存在 blob 的内容一致性，不做覆盖。
		if err := verifyBlob(blobPath, expected); err != nil {
			w.discard()
			return err
		}
		w.discard()
		return nil
	}

	if err := w.file.Close(); err != nil {
		return err
	}
	w.closed = true
	if err := os.Chmod(w.dataPath, 0o444); err != nil {
		return err
	}
	if err := os.Rename(w.dataPath, blobPath); err != nil {
		return err
	}
	if err := os.Remove(w.metaPath); err != nil {
		return err
	}
	return nil
}

// verifyBlob 重新计算已存在 blob 的摘要，校验内容一致。
func verifyBlob(path string, expected Digest) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h, err := expected.Hasher()
	if err != nil {
		return err
	}
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if actual := Digest(fmt.Sprintf("%s:%x", expected.Algorithm(), h.Sum(nil))); actual != expected {
		return fmt.Errorf("content: existing blob %s does not match digest %s", path, expected)
	}
	return nil
}

// discard 关闭并删除会话，不产生任何 blob。
func (w *Writer) discard() {
	if !w.closed {
		w.file.Close()
		w.closed = true
	}
	os.Remove(w.dataPath)
	os.Remove(w.metaPath)
}

// Close 关闭会话但保留已写数据，供之后恢复续传。
func (w *Writer) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true
	if err := w.persist(); err != nil {
		w.file.Close()
		return err
	}
	return w.file.Close()
}

// Reader 打开一个已提交的只读 blob。
func (s *Store) Reader(d Digest) (io.ReadCloser, error) {
	if err := d.Validate(); err != nil {
		return nil, err
	}
	return os.Open(s.blobPath(d))
}

// Get 读取一个已提交 blob 的全部内容。
func (s *Store) Get(d Digest) ([]byte, error) {
	r, err := s.Reader(d)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
}
