package trie

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"sync/atomic"
	"time"
)

var tmpCounter uint64

func (s *Store) shardPath(id NodeID) string {
	hx := id.Hex()
	return filepath.Join(s.dir, dirNodes, hx[:2], hx)
}

func (s *Store) rootsPath() string  { return filepath.Join(s.dir, rootsFile) }
func (s *Store) backupPath() string { return filepath.Join(s.dir, rootsBackup) }

// fsyncDir 对目录做 fsync。rename 的持久性在 fsync 父目录后才成立。
func fsyncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// ensureShard 在需要时创建分片目录；返回 (路径, 是否新建)。
// 新建目录项的持久性由调用方 fsync 父目录（nodes/）保证。
func (s *Store) ensureShard(id NodeID) (string, bool, error) {
	hx := id.Hex()
	shard := filepath.Join(s.dir, dirNodes, hx[:2])
	err := os.Mkdir(shard, 0755)
	if err == nil {
		return shard, true, nil
	}
	if os.IsExist(err) {
		return shard, false, nil
	}
	return "", false, err
}

func tmpName(dir, base string) string {
	n := atomic.AddUint64(&tmpCounter, 1)
	return filepath.Join(dir, fmt.Sprintf("%s%d.%d.%d", tmpPrefix, os.Getpid(), n, time.Now().UnixNano()))
}

// atomicWriteFile 以 "写临时文件 + fsync + rename" 方式落盘。
// beforeRename 在 fsync 之后、rename 之前执行（注入 torn write / 进程被杀）。
func atomicWriteFile(target string, data []byte, perm os.FileMode, beforeRename func() error) error {
	dir := filepath.Dir(target)
	tmp := tmpName(dir, filepath.Base(target))
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	cleanup := true
	defer func() {
		if cleanup {
			f.Close()
			os.Remove(tmp)
		}
	}()
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if beforeRename != nil {
		if err := beforeRename(); err != nil {
			return err
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, target); err != nil {
		return err
	}
	cleanup = false
	return nil
}

// staging 是一次写事务内的暂存：新节点先在内存中算好，提交时统一落盘。
type staging struct {
	store   *Store
	pending map[NodeID]*node
	newRoot NodeID
	seq     uint64
}

func (s *Store) newStaging() *staging {
	return &staging{store: s, pending: make(map[NodeID]*node)}
}

// alloc 登记一个新节点并返回其引用。同一次事务内天然去重。
func (g *staging) alloc(n *node) NodeID {
	enc := n.encode()
	id := hashNode(enc)
	if _, ok := g.pending[id]; !ok {
		g.pending[id] = n
	}
	return id
}

// loadForMutate 加载节点（事务暂存 -> 缓存 -> 磁盘）。
func (g *staging) loadForMutate(id NodeID) (*node, error) {
	if id.IsZero() {
		return nil, nil
	}
	if n, ok := g.pending[id]; ok {
		return n, nil
	}
	return g.store.loadNode(id)
}

// Put 写入键值。value 会被拷贝语义使用（调用方随后修改切片不会影响存储）。
// 返回新根引用。重复写相同值不会分配任何新节点。
func (s *Store) Put(key, value []byte) (NodeID, error) {
	if len(value) > maxValueLen {
		return zeroID, fmt.Errorf("trie: 值长度 %d 超过上限 %d", len(value), maxValueLen)
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	g := s.newStaging()
	val := append([]byte(nil), value...)
	newRoot, changed, err := g.putRec(s.root, key, val)
	if err != nil {
		return zeroID, err
	}
	g.newRoot = newRoot
	g.seq = s.sequence
	if changed {
		g.seq++
	}
	if err := s.commit(g, false); err != nil {
		return zeroID, err
	}
	return newRoot, nil
}

func (g *staging) putRec(id NodeID, key, val []byte) (NodeID, bool, error) {
	n, err := g.loadForMutate(id)
	if err != nil {
		return zeroID, false, err
	}
	if n == nil {
		n = &node{}
	}
	if len(key) == 0 {
		if n.hasVal && string(n.value) == string(val) {
			return id, false, nil
		}
		c := cloneNode(n)
		c.hasVal = true
		c.value = val
		return g.alloc(c), true, nil
	}
	c := cloneNode(n)
	child, changed, err := g.putRec(n.children[key[0]], key[1:], val)
	if err != nil {
		return zeroID, false, err
	}
	if !changed {
		return id, false, nil
	}
	if c.children == nil {
		c.children = make(map[byte]NodeID, 1)
	}
	c.children[key[0]] = child
	return g.alloc(c), true, nil
}

// Delete 删除键。键不存在时返回 ErrNotFound，根不变、不产生任何写入。
func (s *Store) Delete(key []byte) (NodeID, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	g := s.newStaging()
	newRoot, deleted, err := g.delRec(s.root, key)
	if err != nil {
		return zeroID, err
	}
	if !deleted {
		return s.root, ErrNotFound
	}
	g.newRoot = newRoot
	g.seq = s.sequence + 1
	if err := s.commit(g, false); err != nil {
		return zeroID, err
	}
	return newRoot, nil
}

// delRec 执行删除压缩：
// 节点自身的值被移除、且没有任何出边时，节点被剪除（返回零引用）；
// 父节点随之收缩 children。删 "ab" 不会影响 "abc"，因为走到 b 时
// 仍存在到 c 的出边，b 不会被剪除。
func (g *staging) delRec(id NodeID, key []byte) (NodeID, bool, error) {
	if id.IsZero() {
		return zeroID, false, nil
	}
	n, err := g.loadForMutate(id)
	if err != nil {
		return zeroID, false, err
	}
	if n == nil {
		return zeroID, false, nil
	}
	if len(key) == 0 {
		if !n.hasVal {
			return id, false, nil
		}
		c := cloneNode(n)
		c.hasVal = false
		c.value = nil
		if len(c.children) == 0 {
			return zeroID, true, nil // 叶子压缩：直接消失
		}
		return g.alloc(c), true, nil
	}
	childID, ok := n.children[key[0]]
	if !ok {
		return id, false, nil
	}
	newChild, deleted, err := g.delRec(childID, key[1:])
	if err != nil {
		return zeroID, false, err
	}
	if !deleted {
		return id, false, nil
	}
	c := cloneNode(n)
	if newChild.IsZero() {
		delete(c.children, key[0])
	} else {
		c.children[key[0]] = newChild
	}
	if !c.hasVal && len(c.children) == 0 {
		return zeroID, true, nil
	}
	return g.alloc(c), true, nil
}

// manifestFor 构造提交用清单快照。
func (s *Store) manifestFor(root NodeID, seq uint64) diskManifest {
	m := diskManifest{Version: 1, Root: root.Hex(), Sequence: seq, Roots: s.roots}
	if m.Roots == nil {
		m.Roots = map[string]diskRoot{}
	}
	return m
}

// commit 把暂存节点落盘，然后原子切换根。任何注入点失败都使本次
// 内存事务作废；磁盘上只可能多出无主的不可变节点，不影响旧根。
func (s *Store) commit(g *staging, silentHookErr bool) error {
	h := s.hooks
	if err := h.fire(h.BeforeFlushNodes); err != nil {
		return err
	}

	// 新节点按引用排序，保证落盘顺序确定（便于测试与排障）。
	ids := make([]NodeID, 0, len(g.pending))
	for id := range g.pending {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i].Hex() < ids[j].Hex() })

	touchedShards := make(map[string]struct{})
	createdShard := false
	for _, id := range ids {
		target := s.shardPath(id)
		if _, statErr := os.Stat(target); statErr == nil {
			// 同内容节点已存在（历史版本/上次崩溃残留）。读回校验哈希。
			if err := s.verifyExisting(id, target); err != nil {
				return err
			}
			continue
		} else if !os.IsNotExist(statErr) {
			return statErr
		}
		shard, created, err := s.ensureShard(id)
		if err != nil {
			return err
		}
		if created {
			createdShard = true
		}
		data := g.pending[id].encode()
		if err := atomicWriteFile(target, data, 0644, func() error { return h.fire(h.BeforeNodeRename) }); err != nil {
			return err
		}
		s.written++
		touchedShards[shard] = struct{}{}
	}

	if err := h.fire(h.BeforeNodeDirSync); err != nil {
		return err
	}
	if createdShard {
		// 持久化"分片目录被创建"这一目录项变化。
		if err := fsyncDir(filepath.Join(s.dir, dirNodes)); err != nil {
			return err
		}
	}
	dirs := make([]string, 0, len(touchedShards))
	for d := range touchedShards {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	for _, d := range dirs {
		if err := fsyncDir(d); err != nil {
			return err
		}
	}

	// 到此：新节点全部持久且可被引用，但任何根都还不指向它们。
	m := s.manifestFor(g.newRoot, g.seq)
	raw, err := jsonMarshalIndent(m)
	if err != nil {
		return err
	}

	if err := h.fire(h.BeforeRootsWrite); err != nil {
		return err
	}
	if err := atomicWriteFile(s.rootsPath(), raw, 0644, func() error { return h.fire(h.BeforeRootsRename) }); err != nil {
		return err
	}

	// 提交点：roots.json rename 完成，崩溃后重启也会看到新根。
	if err := h.fire(h.BeforeRootsDirSync); err != nil {
		return err
	}
	if err := fsyncDir(s.dir); err != nil {
		return err
	}

	if err := h.fire(h.BeforeRootsBackup); err != nil {
		return err
	}
	// 备份落后于主文件是允许的：主文件损坏时可能回退到上一个已提交根。
	if err := atomicWriteFile(s.backupPath(), raw, 0644, nil); err != nil {
		return err
	}

	// 发布到内存。
	for id, nd := range g.pending {
		s.cache[id] = nd
	}
	s.root = g.newRoot
	s.sequence = g.seq

	if err := h.fire(h.AfterCommit); err != nil {
		if silentHookErr {
			return nil
		}
		return err
	}
	return nil
}

func (s *Store) verifyExisting(id NodeID, path string) error {
	raw, err := ioutil.ReadFile(path)
	if err != nil {
		return err
	}
	if got := hashNode(raw); got != id {
		return newVerifyError(id, "同路径已有内容但哈希不一致", got)
	}
	return nil
}
