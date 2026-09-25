// Package trie 实现磁盘上的不可变路径复制字节前缀树。
//
// 每个树节点以其编码内容的 SHA-256 作为文件名（内容寻址），任何写入都
// 只分配从根到被改叶子这一条路径上的新节点；兄弟子树与历史根继续引用
// 原有节点，因此旧快照永远可读。当前根保存在原子替换的清单文件
// roots.json（外加 .bak 备份）中。
package trie

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	dirNodes    = "nodes"
	rootsFile   = "roots.json"
	rootsBackup = "roots.json.bak"
	tmpPrefix   = ".tmp."
)

// ErrEmptyTree 表示当前（或指定）根是空树：空键之外任何键都不存在。
var ErrEmptyTree = errors.New("trie: 空树")

// ErrNotFound 表示键不存在。
var ErrNotFound = errors.New("trie: 键不存在")

// ErrSnapshotMissing 表示指定标签的快照不存在。
var ErrSnapshotMissing = errors.New("trie: 快照不存在")

// ErrInjected 由注入点返回，表示测试故意制造的"进程崩溃/故障"。
var ErrInjected = errors.New("trie: 注入故障")

// FaultHooks 是提交路径上的故障注入点。
//
// 每个钩子在对应原子步骤执行前被调用；返回非 nil（通常是 ErrInjected）
// 会中止本次事务：内存状态不变，已落盘的不可变节点只会成为无主垃圾
// （下次 Compact 回收），下次 Open 后旧根完整可读。可以用它精确模拟
// "进程在任意时刻被杀掉"（详见 README "崩溃与断电边界"）。
type FaultHooks struct {
	BeforeFlushNodes   func() error // 任何节点写入之前
	BeforeNodeRename   func() error // 每个节点 fsync 完成、rename 之前
	BeforeNodeDirSync  func() error // 节点目录 fsync 之前
	BeforeRootsWrite   func() error // roots.json 临时文件写入之前
	BeforeRootsRename  func() error // roots.json rename 之前
	BeforeRootsDirSync func() error // 目录 fsync（提交点之后）之前
	BeforeRootsBackup  func() error // roots.json.bak 更新之前
	AfterCommit        func() error // 内存中发布新根之后（仅观察用）
}

// RootInfo 描述一个已打标签的根快照。
type RootInfo struct {
	Label    string
	Root     NodeID
	Sequence uint64
	Created  time.Time
}

// Stats 是计数器快照。
type Stats struct {
	NodesWritten uint64 // 自打开以来新写盘并改名的节点数
	NodesRead    uint64 // 自打开以来从磁盘读取的节点数
}

// Store 是前缀树存储。零值不可用，必须通过 Open 创建。
type Store struct {
	dir   string
	mu    sync.RWMutex
	cache map[NodeID]*node // 内容寻址 -> 全局共享、调用方不得修改

	root     NodeID
	sequence uint64
	roots    map[string]diskRoot // label -> 已打标签快照

	written uint64 // 本次打开写计数
	read    uint64 // 本次打开读计数

	hooks FaultHooks
}

type diskRoot struct {
	Root     string    `json:"root"`
	Sequence uint64    `json:"sequence"`
	Created  time.Time `json:"created"`
}

type diskManifest struct {
	Version  int                 `json:"version"`
	Root     string              `json:"root"`
	Sequence uint64              `json:"sequence"`
	Roots    map[string]diskRoot `json:"roots"`
}

// Open 打开（或在空目录上初始化）一个存储。目录由调用方指定。
//
// 打开时清理上次崩溃残留的临时文件，并对清单做结构校验；节点只在被
// 访问时惰性验证（内容寻址 + 遍历校验见 Verify）。
func Open(dir string) (*Store, error) {
	for _, sub := range []string{"", dirNodes} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0755); err != nil {
			return nil, fmt.Errorf("trie: 创建目录失败: %v", err)
		}
	}
	s := &Store{
		dir:   dir,
		cache: make(map[NodeID]*node),
		roots: make(map[string]diskRoot),
	}
	if err := s.sweepTempFiles(); err != nil {
		return nil, err
	}
	if err := s.loadManifest(); err != nil {
		return nil, err
	}
	return s, nil
}

// Close 释放存储。当前实现没有后台资源，保留接口供调用方统一管理生命周期。
func (s *Store) Close() error { return nil }

func (s *Store) sweepTempFiles() error {
	clean := func(dir string) error {
		fis, err := ioutil.ReadDir(dir)
		if err != nil {
			return err
		}
		for _, fi := range fis {
			if strings.HasPrefix(fi.Name(), tmpPrefix) {
				if err := os.Remove(filepath.Join(dir, fi.Name())); err != nil && !os.IsNotExist(err) {
					return err
				}
			}
		}
		return nil
	}
	if err := clean(s.dir); err != nil {
		return fmt.Errorf("trie: 清理临时文件失败: %v", err)
	}
	ndir := filepath.Join(s.dir, dirNodes)
	fis, err := ioutil.ReadDir(ndir)
	if err != nil {
		return fmt.Errorf("trie: 读取节点目录失败: %v", err)
	}
	for _, fi := range fis {
		if fi.IsDir() {
			if err := clean(filepath.Join(ndir, fi.Name())); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("trie: 清理临时文件失败: %v", err)
			}
		}
	}
	return nil
}

func parseManifest(raw []byte) (*diskManifest, NodeID, error) {
	var m diskManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, zeroID, fmt.Errorf("trie: roots.json 解析失败: %v", err)
	}
	if m.Version != 1 {
		return nil, zeroID, fmt.Errorf("trie: roots.json 版本 %d 不受支持", m.Version)
	}
	if m.Roots == nil {
		m.Roots = map[string]diskRoot{}
	}
	root, err := ParseNodeID(m.Root)
	if err != nil {
		return nil, zeroID, fmt.Errorf("trie: roots.json 当前根非法: %v", err)
	}
	for label, r := range m.Roots {
		if _, err := ParseNodeID(r.Root); err != nil {
			return nil, zeroID, fmt.Errorf("trie: roots.json 快照 %q 引用非法: %v", label, err)
		}
	}
	return &m, root, nil
}

func (s *Store) loadManifest() error {
	primary, perr := ioutil.ReadFile(filepath.Join(s.dir, rootsFile))
	var m *diskManifest
	var root NodeID
	if perr == nil {
		m, root, perr = parseManifest(primary)
	}
	switch {
	case perr == nil:
		// 主文件正常，无需备份。
	case os.IsNotExist(perr) && primary == nil:
		// 全新仓库。
		return nil
	default:
		backup, berr := ioutil.ReadFile(filepath.Join(s.dir, rootsBackup))
		if berr != nil {
			if os.IsNotExist(berr) {
				return fmt.Errorf("trie: roots.json 损坏且无备份: %v", perr)
			}
			return fmt.Errorf("trie: 读取 roots.json.bak 失败: %v", berr)
		}
		m, root, berr = parseManifest(backup)
		if berr != nil {
			return fmt.Errorf("trie: roots.json 与备份均损坏: 主: %v; 备份: %v", perr, berr)
		}
	}
	s.root = root
	s.sequence = m.Sequence
	s.roots = m.Roots
	return nil
}

// ---- 故障注入 ----

// SetFaultHooks 安装故障注入钩子（传 FaultHooks{} 清除）。
func (s *Store) SetFaultHooks(h FaultHooks) {
	s.mu.Lock()
	s.hooks = h
	s.mu.Unlock()
}

func (h FaultHooks) fire(fn func() error) error {
	if fn == nil {
		return nil
	}
	return fn()
}

// Stats 返回本次打开以来的 I/O 计数。
func (s *Store) Stats() Stats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return Stats{NodesWritten: s.written, NodesRead: s.read}
}
