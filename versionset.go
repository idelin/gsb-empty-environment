package versionset

import (
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// NumLevels 是 LSM 树的层数。
const NumLevels = 7

const (
	currentFileName  = "CURRENT"
	manifestPrefix   = "MANIFEST-"
	sstSuffix        = ".sst"
	initialManifestN = 1
)

// FileMeta 描述一个 SSTable 文件及其内容校验和。
type FileMeta struct {
	Level    int
	FileNum  uint64
	Size     uint64
	Checksum uint32 // 文件内容的 CRC32
	Smallest []byte
	Largest  []byte
}

// Version 是某一时刻各层 SSTable 文件列表的不可变快照。
type Version struct {
	Files [NumLevels][]*FileMeta // 每层按 FileNum 升序
}

func (v *Version) clone() *Version {
	nv := &Version{}
	for l := 0; l < NumLevels; l++ {
		nv.Files[l] = append([]*FileMeta(nil), v.Files[l]...)
	}
	return nv
}

// apply 将一条 VersionEdit 作用到版本上（原地修改）。
func (v *Version) apply(e *VersionEdit) {
	for _, d := range e.DeletedFiles {
		files := v.Files[d.Level]
		out := files[:0]
		for _, f := range files {
			if f.FileNum != d.FileNum {
				out = append(out, f)
			}
		}
		v.Files[d.Level] = out
	}
	for i := range e.NewFiles {
		f := e.NewFiles[i]
		fm := f // 拷贝，避免共享底层数组
		v.Files[f.Level] = append(v.Files[f.Level], &fm)
	}
	for l := 0; l < NumLevels; l++ {
		files := v.Files[l]
		sort.Slice(files, func(i, j int) bool { return files[i].FileNum < files[j].FileNum })
	}
}

// liveSet 返回当前版本引用的文件号集合。
func (v *Version) liveSet() map[uint64]bool {
	live := make(map[uint64]bool)
	for l := 0; l < NumLevels; l++ {
		for _, f := range v.Files[l] {
			live[f.FileNum] = true
		}
	}
	return live
}

// VersionSet 管理当前版本与 MANIFEST 日志，保证版本演进的原子性。
type VersionSet struct {
	dir         string
	mu          sync.Mutex
	current     *Version
	manifest    *os.File
	manifestNum uint64
	nextFileNum uint64
}

// Open 打开（必要时创建）dir 下的 VersionSet。
// 恢复流程：读 CURRENT -> 重放 MANIFEST -> 校验存活文件校验和 -> 清理孤儿文件。
func Open(dir string) (*VersionSet, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	vs := &VersionSet{dir: dir, current: &Version{}, nextFileNum: 2}

	manifestName, err := readCurrent(dir)
	if os.IsNotExist(err) {
		// 全新目录：创建初始 MANIFEST 并原子写入 CURRENT。
		vs.manifestNum = initialManifestN
		if err := vs.createManifest(); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	} else {
		// 已存在：解析 MANIFEST-NNNNNN 编号并重放。
		num, perr := strconv.ParseUint(strings.TrimPrefix(manifestName, manifestPrefix), 10, 64)
		if perr != nil || !strings.HasPrefix(manifestName, manifestPrefix) {
			return nil, fmt.Errorf("versionset: bad CURRENT contents %q", manifestName)
		}
		vs.manifestNum = num
		if err := vs.recover(); err != nil {
			return nil, err
		}
	}

	// 追加模式打开 MANIFEST，供后续 LogAndApply 使用。
	mf, err := os.OpenFile(vs.manifestPath(), os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	vs.manifest = mf
	return vs, nil
}

// createManifest 写入初始 MANIFEST（含 nextFileNum），再原子切换 CURRENT。
func (vs *VersionSet) createManifest() error {
	path := vs.manifestPath()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	edit := &VersionEdit{HasNextFileNum: true, NextFileNum: vs.nextFileNum}
	if err := appendRecord(f, edit.encode()); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil { // 日志落盘后才允许 CURRENT 指向它
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return writeCurrentAtomic(vs.dir, filepath.Base(path))
}

// recover 重放 MANIFEST，校验存活文件，清理孤儿 SSTable。
func (vs *VersionSet) recover() error {
	data, err := os.ReadFile(vs.manifestPath())
	if err != nil {
		return err
	}
	rr := newRecordReader(strings.NewReader(string(data)))
	v := &Version{}
	nextFileNum := uint64(0)
	for {
		payload, err := rr.next()
		if err == io.EOF {
			break
		}
		if err == errCorruptTail {
			// 崩溃留下的撕裂记录：截断到最后的完好位置。
			if terr := os.Truncate(vs.manifestPath(), rr.LastGood); terr != nil {
				return terr
			}
			break
		}
		if err != nil {
			return err
		}
		var edit VersionEdit
		if err := edit.decode(payload); err != nil {
			return err
		}
		v.apply(&edit)
		if edit.HasNextFileNum {
			nextFileNum = edit.NextFileNum
		}
	}
	vs.current = v
	if nextFileNum > vs.nextFileNum {
		vs.nextFileNum = nextFileNum
	}
	// 崩溃恢复：确认存活文件完好，并删除未被任何版本引用的孤儿文件。
	if err := vs.verifyLiveFiles(); err != nil {
		return err
	}
	return vs.removeOrphanFiles()
}

// verifyLiveFiles 校验当前版本引用的每个 SSTable 存在且校验和匹配。
func (vs *VersionSet) verifyLiveFiles() error {
	for l := 0; l < NumLevels; l++ {
		for _, f := range vs.current.Files[l] {
			data, err := os.ReadFile(vs.sstPath(f.FileNum))
			if err != nil {
				return fmt.Errorf("versionset: live file %d missing: %w", f.FileNum, err)
			}
			if crc32.ChecksumIEEE(data) != f.Checksum {
				return fmt.Errorf("versionset: live file %d checksum mismatch", f.FileNum)
			}
		}
	}
	return nil
}

// removeOrphanFiles 删除目录中未被当前版本引用的 *.sst 文件。
// 这正是"Compaction 写出新文件后、提交前崩溃"场景的恢复动作。
func (vs *VersionSet) removeOrphanFiles() error {
	live := vs.current.liveSet()
	entries, err := os.ReadDir(vs.dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, sstSuffix) {
			continue
		}
		num, err := strconv.ParseUint(strings.TrimSuffix(name, sstSuffix), 10, 64)
		if err != nil {
			continue
		}
		if !live[num] {
			if err := os.Remove(filepath.Join(vs.dir, name)); err != nil {
				return err
			}
		}
	}
	return syncDir(vs.dir)
}

// LogAndApply 将一条 VersionEdit 原子地追加到 MANIFEST 并应用到当前版本。
// 先写日志并 fsync，再切换内存版本——崩溃只会丢失未 fsync 的尾部，
// 恢复时由重放逻辑截断，已提交的版本始终完整。
func (vs *VersionSet) LogAndApply(edit *VersionEdit) error {
	vs.mu.Lock()
	defer vs.mu.Unlock()

	edit.HasNextFileNum = true
	edit.NextFileNum = vs.nextFileNum

	if err := appendRecord(vs.manifest, edit.encode()); err != nil {
		return err
	}
	if err := vs.manifest.Sync(); err != nil { // 提交点：fsync 后版本演进生效
		return err
	}

	before := vs.current.liveSet()
	nv := vs.current.clone()
	nv.apply(edit)
	vs.current = nv

	// 提交成功后尽力清理不再被引用的旧文件；
	// 若此处崩溃，恢复时的 removeOrphanFiles 会兜底。
	after := nv.liveSet()
	for num := range before {
		if !after[num] {
			os.Remove(vs.sstPath(num))
		}
	}
	return syncDir(vs.dir)
}

// NextFileNum 分配一个新的文件号。
func (vs *VersionSet) NextFileNum() uint64 {
	vs.mu.Lock()
	defer vs.mu.Unlock()
	n := vs.nextFileNum
	vs.nextFileNum++
	return n
}

// Current 返回当前版本的快照。
func (vs *VersionSet) Current() *Version {
	vs.mu.Lock()
	defer vs.mu.Unlock()
	return vs.current.clone()
}

// Close 关闭 MANIFEST。
func (vs *VersionSet) Close() error {
	vs.mu.Lock()
	defer vs.mu.Unlock()
	if vs.manifest != nil {
		err := vs.manifest.Close()
		vs.manifest = nil
		return err
	}
	return nil
}

// WriteSSTable 把 data 写入 <num>.sst 并 fsync，返回带校验和的 FileMeta。
// 模拟 Compaction 输出文件的落盘过程。
func (vs *VersionSet) WriteSSTable(fileNum uint64, level int, data, smallest, largest []byte) (*FileMeta, error) {
	path := vs.sstPath(fileNum)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return nil, err
	}
	if err := f.Sync(); err != nil { // 文件内容先于元数据提交落盘
		f.Close()
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	if err := syncDir(vs.dir); err != nil {
		return nil, err
	}
	return &FileMeta{
		Level:    level,
		FileNum:  fileNum,
		Size:     uint64(len(data)),
		Checksum: crc32.ChecksumIEEE(data),
		Smallest: smallest,
		Largest:  largest,
	}, nil
}

func (vs *VersionSet) manifestPath() string {
	return filepath.Join(vs.dir, fmt.Sprintf("%s%06d", manifestPrefix, vs.manifestNum))
}

func (vs *VersionSet) sstPath(num uint64) string {
	return filepath.Join(vs.dir, fmt.Sprintf("%06d%s", num, sstSuffix))
}

// readCurrent 读取 CURRENT 文件内容。
func readCurrent(dir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(dir, currentFileName))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// writeCurrentAtomic 通过 临时文件+rename 原子更新 CURRENT，并 fsync 目录。
func writeCurrentAtomic(dir, manifestName string) error {
	tmp := filepath.Join(dir, "CURRENT.tmp")
	if err := os.WriteFile(tmp, []byte(manifestName+"\n"), 0o644); err != nil {
		return err
	}
	if f, err := os.Open(tmp); err == nil { // fsync 临时文件内容
		f.Sync()
		f.Close()
	}
	if err := os.Rename(tmp, filepath.Join(dir, currentFileName)); err != nil {
		return err
	}
	return syncDir(dir)
}

// syncDir fsync 目录本身，保证目录项（新建/重命名/删除）持久化。
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
