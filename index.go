// Package treeindex 维护一个持久化的 对象ID -> 多级路径 索引。
//
// 特性：
//   - 批次（[]Op）原子提交：全部校验通过才落盘，任一失败则整批拒绝；
//   - 移动子树前做循环检测（把 A 移到其子目录 B 会被拒绝）；
//   - 同级重名冲突检测在批次内顺序应用的工作副本上进行，
//     因此同批内先后的重名不会因顺序而漏检；
//   - 路径缓存按版本失效：每次成功提交 version+1，旧版本缓存条目作废；
//   - ID 与名称解耦，重命名/移动不改变 ID；
//   - 崩溃恢复：WAL（journal）+ 快照（snapshot），Open 时重放未 checkpoint 的日志。
package treeindex

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// RootID 是虚拟根节点的固定 ID，路径为 "/"。
const RootID = "root"

var (
	ErrNotFound      = errors.New("treeindex: node not found")
	ErrExists        = errors.New("treeindex: node id already exists")
	ErrCycle         = errors.New("treeindex: move would create a cycle")
	ErrNameConflict  = errors.New("treeindex: sibling name conflict")
	ErrRootImmutable = errors.New("treeindex: root cannot be moved or renamed")
	ErrInvalidName   = errors.New("treeindex: invalid name")
)

// 存储文件布局（全部位于调用方指定的目录内）。
const (
	snapshotFile = "snapshot.json" // 已 checkpoint 的全量状态
	snapshotTmp  = "snapshot.tmp"  // 快照写入中的临时文件（崩溃后丢弃）
	journalFile  = "journal.log"   // 已提交但未 checkpoint 的批次（WAL）
	journalTmp   = "journal.tmp"   // 日志写入中的临时文件（崩溃后丢弃）
)

// 故障注入点。通过 WithHook 注入返回错误的函数即可模拟该时刻的崩溃/断电。
const (
	HookBeforeJournalTmp    = "journal.beforeTmpWrite" // 写 journal.tmp 之前
	HookBeforeJournalRename = "journal.beforeRename"   // journal.tmp 原子改名前
	HookAfterJournalCommit  = "journal.afterCommit"    // 日志已提交（rename+fsync）之后
	HookBeforeSnapshotWrite = "snapshot.beforeWrite"   // 写快照之前
	HookBeforeJournalRemove = "journal.beforeRemove"   // checkpoint 后删除日志前
)

// Node 是索引中的一个节点。ID 恒定，Parent/Name 可变。
type Node struct {
	ID     string `json:"id"`
	Parent string `json:"parent"`
	Name   string `json:"name"`
}

// OpKind 是批次中单个操作的类型。
type OpKind string

const (
	OpCreate OpKind = "create"
	OpMove   OpKind = "move"
	OpRename OpKind = "rename"
)

// Op 是原子批次中的一个操作。
type Op struct {
	Kind     OpKind `json:"kind"`
	ID       string `json:"id"`
	ParentID string `json:"parent_id,omitempty"` // Create/Move 使用
	Name     string `json:"name,omitempty"`      // Create/Rename 使用
}

// Create 构造一个创建节点的操作。
func Create(id, parentID, name string) Op {
	return Op{Kind: OpCreate, ID: id, ParentID: parentID, Name: name}
}

// Move 构造一个把 id 子树整体移动到 newParentID 下的操作。
func Move(id, newParentID string) Op { return Op{Kind: OpMove, ID: id, ParentID: newParentID} }

// Rename 构造一个重命名操作（ID 不变）。
func Rename(id, newName string) Op { return Op{Kind: OpRename, ID: id, Name: newName} }

// cachedPath 是带版本号的路径缓存条目；版本不等于当前版本即失效。
type cachedPath struct {
	version uint64
	path    string
}

// Index 是 ID -> 路径 索引。所有状态文件都位于构造时指定的目录中。
type Index struct {
	mu       sync.Mutex
	dir      string
	nodes    map[string]*Node
	version  uint64
	cache    map[string]cachedPath
	hooks    map[string]func() error
	recovery bool // Open 恢复期间不触发故障注入
}

// Option 用于 Open 的可选配置。
type Option func(*Index)

// WithHook 在指定注入点挂载故障函数；返回非 nil error 即模拟该点发生故障。
func WithHook(point string, fn func() error) Option {
	return func(ix *Index) { ix.hooks[point] = fn }
}

type diskState struct {
	Version uint64           `json:"version"`
	Nodes   map[string]*Node `json:"nodes"`
}

type journalEntry struct {
	Batch []Op `json:"batch"`
}

// Open 打开（必要时创建）dir 下的索引，并执行崩溃恢复：
// 丢弃不完整的临时文件，重放已提交但未 checkpoint 的日志。
func Open(dir string, opts ...Option) (*Index, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	ix := &Index{
		dir:   dir,
		nodes: make(map[string]*Node),
		cache: make(map[string]cachedPath),
		hooks: make(map[string]func() error),
	}
	for _, o := range opts {
		o(ix)
	}

	// 临时文件一定是不完整的（完整写入后会原子 rename），直接丢弃。
	os.Remove(filepath.Join(ix.dir, snapshotTmp))
	os.Remove(filepath.Join(ix.dir, journalTmp))

	ix.recovery = true
	defer func() { ix.recovery = false }()

	data, err := os.ReadFile(filepath.Join(ix.dir, snapshotFile))
	switch {
	case errors.Is(err, os.ErrNotExist):
		ix.nodes[RootID] = &Node{ID: RootID}
	case err != nil:
		return nil, err
	default:
		var st diskState
		if err := json.Unmarshal(data, &st); err != nil {
			return nil, fmt.Errorf("treeindex: corrupt snapshot: %w", err)
		}
		ix.nodes = st.Nodes
		ix.version = st.Version
	}

	// 重放 WAL：日志存在说明对应批次已提交（rename+fsync 完成），必须生效。
	data, err = os.ReadFile(filepath.Join(ix.dir, journalFile))
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, err
	default:
		var je journalEntry
		if err := json.Unmarshal(data, &je); err != nil {
			return nil, fmt.Errorf("treeindex: corrupt journal: %w", err)
		}
		next, err := applyBatch(ix.nodes, je.Batch)
		if err != nil {
			return nil, fmt.Errorf("treeindex: journal replay failed: %w", err)
		}
		ix.nodes = next
		ix.version++
		if err := ix.checkpointLocked(); err != nil {
			return nil, err
		}
	}
	return ix, nil
}

// Commit 原子地应用一个批次：先在内存工作副本上顺序校验全部操作
// （循环、重名、存在性），任一失败则整批拒绝且内存与磁盘均不变；
// 全部通过后写 WAL（fsync + 原子 rename + 目录 fsync），再应用到内存，
// 最后 checkpoint（写快照、删日志）。
func (ix *Index) Commit(batch []Op) error {
	ix.mu.Lock()
	defer ix.mu.Unlock()

	next, err := applyBatch(ix.nodes, batch)
	if err != nil {
		return err // 校验失败：未触碰任何持久化状态与内存状态
	}
	if err := ix.writeJournalLocked(batch); err != nil {
		return err
	}
	// 日志已持久化，此刻起本批次保证最终生效（即使随后崩溃）。
	ix.nodes = next
	ix.version++
	if err := ix.checkpointLocked(); err != nil {
		return err
	}
	return nil
}

// applyBatch 在 nodes 的深拷贝上顺序应用并校验整个批次。
// 因为同批前面的操作对工作副本可见，所以兄弟重名不会因批次顺序漏检。
func applyBatch(nodes map[string]*Node, batch []Op) (map[string]*Node, error) {
	next := make(map[string]*Node, len(nodes)+len(batch))
	for id, n := range nodes {
		cp := *n
		next[id] = &cp
	}
	for i, op := range batch {
		if err := applyOp(next, op); err != nil {
			return nil, fmt.Errorf("op %d (%s %q): %w", i, op.Kind, op.ID, err)
		}
	}
	return next, nil
}

func applyOp(nodes map[string]*Node, op Op) error {
	switch op.Kind {
	case OpCreate:
		if _, ok := nodes[op.ID]; ok {
			return ErrExists
		}
		if err := validName(op.Name); err != nil {
			return err
		}
		if _, ok := nodes[op.ParentID]; !ok {
			return fmt.Errorf("parent %q: %w", op.ParentID, ErrNotFound)
		}
		if siblingConflict(nodes, op.ParentID, op.Name, "") {
			return ErrNameConflict
		}
		nodes[op.ID] = &Node{ID: op.ID, Parent: op.ParentID, Name: op.Name}

	case OpMove:
		n, ok := nodes[op.ID]
		if !ok {
			return ErrNotFound
		}
		if op.ID == RootID {
			return ErrRootImmutable
		}
		if _, ok := nodes[op.ParentID]; !ok {
			return fmt.Errorf("parent %q: %w", op.ParentID, ErrNotFound)
		}
		// 循环检测：新父节点不能是被移动节点自身或其后代，
		// 否则 A 会被移到自己的子目录 B 下。
		if isSelfOrDescendant(nodes, op.ID, op.ParentID) {
			return ErrCycle
		}
		if n.Parent != op.ParentID && siblingConflict(nodes, op.ParentID, n.Name, op.ID) {
			return ErrNameConflict
		}
		n.Parent = op.ParentID

	case OpRename:
		n, ok := nodes[op.ID]
		if !ok {
			return ErrNotFound
		}
		if op.ID == RootID {
			return ErrRootImmutable
		}
		if err := validName(op.Name); err != nil {
			return err
		}
		if siblingConflict(nodes, n.Parent, op.Name, op.ID) {
			return ErrNameConflict
		}
		n.Name = op.Name

	default:
		return fmt.Errorf("unknown op kind %q", op.Kind)
	}
	return nil
}

func validName(name string) error {
	if name == "" || strings.Contains(name, "/") {
		return ErrInvalidName
	}
	return nil
}

// siblingConflict 检查 parentID 下是否已有名为 name 的其他节点。
func siblingConflict(nodes map[string]*Node, parentID, name, selfID string) bool {
	for _, n := range nodes {
		if n.ID != selfID && n.Parent == parentID && n.Name == name {
			return true
		}
	}
	return false
}

// isSelfOrDescendant 判断 cur 是否等于 ancestor 或是 ancestor 的后代（沿父链向上走）。
func isSelfOrDescendant(nodes map[string]*Node, ancestor, cur string) bool {
	for cur != "" {
		if cur == ancestor {
			return true
		}
		n, ok := nodes[cur]
		if !ok {
			return false
		}
		cur = n.Parent
	}
	return false
}

// writeJournalLocked 把批次写入 WAL 并确保持久化。
func (ix *Index) writeJournalLocked(batch []Op) error {
	if err := ix.hook(HookBeforeJournalTmp); err != nil {
		return err
	}
	data, err := json.Marshal(journalEntry{Batch: batch})
	if err != nil {
		return err
	}
	if err := writeFileSync(filepath.Join(ix.dir, journalTmp), data); err != nil {
		return err
	}
	if err := ix.hook(HookBeforeJournalRename); err != nil {
		return err
	}
	if err := os.Rename(
		filepath.Join(ix.dir, journalTmp),
		filepath.Join(ix.dir, journalFile),
	); err != nil {
		return err
	}
	if err := syncDir(ix.dir); err != nil {
		return err
	}
	// 提交点：此后崩溃，Open 会重放该日志。
	return ix.hook(HookAfterJournalCommit)
}

// checkpointLocked 把当前内存状态写为快照并删除 WAL。
func (ix *Index) checkpointLocked() error {
	if err := ix.hook(HookBeforeSnapshotWrite); err != nil {
		return err
	}
	data, err := json.Marshal(diskState{Version: ix.version, Nodes: ix.nodes})
	if err != nil {
		return err
	}
	if err := writeFileSync(filepath.Join(ix.dir, snapshotTmp), data); err != nil {
		return err
	}
	if err := os.Rename(
		filepath.Join(ix.dir, snapshotTmp),
		filepath.Join(ix.dir, snapshotFile),
	); err != nil {
		return err
	}
	if err := syncDir(ix.dir); err != nil {
		return err
	}
	if err := ix.hook(HookBeforeJournalRemove); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(ix.dir, journalFile)); err != nil &&
		!errors.Is(err, os.ErrNotExist) {
		return err
	}
	return syncDir(ix.dir)
}

func (ix *Index) hook(point string) error {
	if ix.recovery {
		return nil
	}
	if fn, ok := ix.hooks[point]; ok && fn != nil {
		return fn()
	}
	return nil
}

// Path 返回 id 的绝对路径（如 "/a/b/c"）。结果按当前版本缓存，
// 版本变化（任何成功提交）后旧缓存自动失效。
func (ix *Index) Path(id string) (string, error) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	return ix.pathLocked(id)
}

func (ix *Index) pathLocked(id string) (string, error) {
	if c, ok := ix.cache[id]; ok && c.version == ix.version {
		return c.path, nil
	}
	if _, ok := ix.nodes[id]; !ok {
		return "", ErrNotFound
	}
	var parts []string
	for cur := id; cur != RootID; {
		n := ix.nodes[cur]
		parts = append(parts, n.Name)
		cur = n.Parent
	}
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	p := "/" + strings.Join(parts, "/")
	ix.cache[id] = cachedPath{version: ix.version, path: p}
	return p, nil
}

// Version 返回当前索引版本号；每次成功提交（含恢复重放）递增。
func (ix *Index) Version() uint64 {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	return ix.version
}

// Node 返回 id 对应节点的副本。
func (ix *Index) Node(id string) (Node, bool) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	n, ok := ix.nodes[id]
	if !ok {
		return Node{}, false
	}
	return *n, true
}

// IDs 返回全部节点 ID（含根），按字典序排序。
func (ix *Index) IDs() []string {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	ids := make([]string, 0, len(ix.nodes))
	for id := range ix.nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func writeFileSync(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
