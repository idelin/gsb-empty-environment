// Package backup 基于 SSTable 不可变性实现 LSM 引擎的物理备份：
// 通过硬链接在毫秒级导出零 IO 拷贝的一致性快照，并记录增量
// SSTable 清单以支持差异化离线归档。
package backup

import (
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"snapshotlsm/lsm"
)

const (
	// ManifestFile 是快照目录内的全量清单文件名。
	ManifestFile = "MANIFEST.json"
	// DeltaFile 是快照目录内的增量清单文件名（相对上一快照新增的 SSTable）。
	DeltaFile = "DELTA.json"
)

// FileEntry 描述清单中的一个 SSTable 文件及其校验信息。
type FileEntry struct {
	Name  string `json:"name"`
	Size  int64  `json:"size"`
	CRC32 uint32 `json:"crc32"`
}

// Manifest 是一次快照的全量/增量 SSTable 清单。
type Manifest struct {
	Seq       int         `json:"seq"`
	CreatedAt time.Time   `json:"created_at"`
	Files     []FileEntry `json:"files"`
}

// Snapshotter 对同一个 DB 执行全量硬链接快照并维护增量清单。
type Snapshotter struct {
	db  *lsm.DB
	dir string

	mu   sync.Mutex
	seq  int
	prev *Manifest
}

// NewSnapshotter 创建快照器，快照根目录为 dir。
// 若目录中已有历史快照，则在其基础上继续递增序号与增量链。
func NewSnapshotter(db *lsm.DB, dir string) (*Snapshotter, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	s := &Snapshotter{db: db, dir: dir}
	fis, err := ioutil.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, fi := range fis {
		if !fi.IsDir() || !strings.HasPrefix(fi.Name(), "snap-") {
			continue
		}
		n, err := strconv.Atoi(strings.TrimPrefix(fi.Name(), "snap-"))
		if err != nil {
			continue
		}
		if n > s.seq {
			s.seq = n
		}
	}
	if s.seq > 0 {
		m, err := LoadManifest(filepath.Join(dir, snapshotDirName(s.seq), ManifestFile))
		if err != nil {
			return nil, fmt.Errorf("load previous manifest: %v", err)
		}
		s.prev = m
	}
	return s, nil
}

func snapshotDirName(seq int) string { return fmt.Sprintf("snap-%06d", seq) }

// Snapshot 生成一次一致性快照：
//  1. 在 DB 读锁保护下枚举当前全部 SSTable；
//  2. 为每个 SSTable 在快照目录中建立硬链接（零数据拷贝）；
//  3. 写入全量清单 MANIFEST.json（含 size 与 CRC32）；
//  4. 与上一快照对比，写入增量清单 DELTA.json（仅新增 SSTable）。
func (s *Snapshotter) Snapshot() (*Manifest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	seq := s.seq + 1
	snapDir := filepath.Join(s.dir, snapshotDirName(seq))
	if err := os.MkdirAll(snapDir, 0755); err != nil {
		return nil, err
	}

	m := &Manifest{Seq: seq, CreatedAt: time.Now()}
	err := s.db.ViewSSTables(func(files []string) error {
		for _, src := range files {
			dst := filepath.Join(snapDir, filepath.Base(src))
			if err := os.Link(src, dst); err != nil {
				return fmt.Errorf("hard link %s: %v", src, err)
			}
			fi, err := os.Stat(dst)
			if err != nil {
				return err
			}
			crc, err := CRCFile(dst)
			if err != nil {
				return err
			}
			m.Files = append(m.Files, FileEntry{
				Name:  filepath.Base(src),
				Size:  fi.Size(),
				CRC32: crc,
			})
		}
		return nil
	})
	if err != nil {
		os.RemoveAll(snapDir)
		return nil, err
	}
	sort.Slice(m.Files, func(i, j int) bool { return m.Files[i].Name < m.Files[j].Name })

	if err := writeJSON(filepath.Join(snapDir, ManifestFile), m); err != nil {
		return nil, err
	}

	// 增量清单：与上一快照求差集（SSTable 文件名单调递增，新文件即增量）。
	delta := &Manifest{Seq: seq, CreatedAt: m.CreatedAt}
	prevNames := make(map[string]bool)
	if s.prev != nil {
		for _, fe := range s.prev.Files {
			prevNames[fe.Name] = true
		}
	}
	for _, fe := range m.Files {
		if !prevNames[fe.Name] {
			delta.Files = append(delta.Files, fe)
		}
	}
	if err := writeJSON(filepath.Join(snapDir, DeltaFile), delta); err != nil {
		return nil, err
	}

	s.seq = seq
	s.prev = m
	return m, nil
}

// SnapshotDir 返回指定序号快照的目录。
func (s *Snapshotter) SnapshotDir(seq int) string {
	return filepath.Join(s.dir, snapshotDirName(seq))
}

// LoadManifest 读取清单文件。
func LoadManifest(path string) (*Manifest, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// Verify 校验快照目录：逐个文件重算 CRC32 并与清单比对，
// 用于确认快照数据在源文件被 compaction 删除后依然完整。
func Verify(snapDir string) error {
	m, err := LoadManifest(filepath.Join(snapDir, ManifestFile))
	if err != nil {
		return err
	}
	for _, fe := range m.Files {
		path := filepath.Join(snapDir, fe.Name)
		fi, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("verify %s: %v", fe.Name, err)
		}
		if fi.Size() != fe.Size {
			return fmt.Errorf("verify %s: size %d != manifest %d", fe.Name, fi.Size(), fe.Size)
		}
		crc, err := CRCFile(path)
		if err != nil {
			return fmt.Errorf("verify %s: %v", fe.Name, err)
		}
		if crc != fe.CRC32 {
			return fmt.Errorf("verify %s: crc %08x != manifest %08x", fe.Name, crc, fe.CRC32)
		}
	}
	return nil
}

// CRCFile 计算文件的 CRC32。
func CRCFile(path string) (uint32, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	h := crc32.NewIEEE()
	if _, err := io.Copy(h, f); err != nil {
		return 0, err
	}
	return h.Sum32(), nil
}

// SameFile 报告两个路径是否指向同一 inode（即互为硬链接，零拷贝）。
func SameFile(a, b string) (bool, error) {
	fia, err := os.Stat(a)
	if err != nil {
		return false, err
	}
	fib, err := os.Stat(b)
	if err != nil {
		return false, err
	}
	sta, ok1 := fia.Sys().(*syscall.Stat_t)
	stb, ok2 := fib.Sys().(*syscall.Stat_t)
	if !ok1 || !ok2 {
		return false, fmt.Errorf("unsupported platform for inode comparison")
	}
	return sta.Ino == stb.Ino && sta.Dev == stb.Dev, nil
}

func writeJSON(path string, v interface{}) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return ioutil.WriteFile(path, data, 0644)
}
