package trie

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"time"
)

// Root 返回当前根引用与单调递增的序列号。
func (s *Store) Root() (NodeID, uint64) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.root, s.sequence
}

// Snapshots 返回全部已打标签快照（按标签排序）。
func (s *Store) Snapshots() []RootInfo {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]RootInfo, 0, len(s.roots))
	for label, r := range s.roots {
		id, _ := ParseNodeID(r.Root)
		out = append(out, RootInfo{Label: label, Root: id, Sequence: r.Sequence, Created: r.Created})
	}
	// RootInfo 无标签顺序字段，按标签排稳定输出。
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1].Label > out[j].Label; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}

// Snapshot 给指定根打标签；root 传零值时打在当前根上。
// 标签已存在则覆盖（指向新的根）。历史根引用也可以打标签，
// 从而在 Compact 中被保留。
func (s *Store) Snapshot(label string, root NodeID) (NodeID, error) {
	if label == "" {
		return zeroID, fmt.Errorf("trie: 快照标签不能为空")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if root.IsZero() {
		root = s.root
	}
	if root.IsZero() {
		return zeroID, ErrEmptyTree
	}
	// 解析/校验该根可达，避免给损坏引用打标签。
	probe := make(map[NodeID]struct{})
	if err := s.markReachable(root, probe); err != nil {
		return zeroID, err
	}
	// 打标签不产生新节点，只重写清单（序列号不增加：树版本未变）。
	g := &staging{store: s, pending: map[NodeID]*node{}, newRoot: s.root, seq: s.sequence}
	s.roots[label] = diskRoot{Root: root.Hex(), Sequence: s.sequence, Created: time.Now().UTC()}
	if err := s.commit(g, true); err != nil {
		delete(s.roots, label)
		return zeroID, err
	}
	return root, nil
}

// DeleteSnapshot 删除标签（不影响任何节点）。
func (s *Store) DeleteSnapshot(label string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.roots[label]; !ok {
		return ErrSnapshotMissing
	}
	delete(s.roots, label)
	m := s.manifestFor(s.root, s.sequence)
	raw, err := jsonMarshalIndent(m)
	if err != nil {
		return err
	}
	if err := atomicWriteFile(s.rootsPath(), raw, 0644, nil); err != nil {
		return err
	}
	if err := fsyncDir(s.dir); err != nil {
		return err
	}
	return atomicWriteFile(s.backupPath(), raw, 0644, nil)
}

// Compact 执行删除垃圾回收：从当前根和所有已打标签快照出发标记可达节点，
// 删除其余节点文件（历史提交留下的旧版本节点、崩溃残留的无主节点）。
// 清理前对每个保留根做完整遍历校验；发现损坏引用则中止压缩并返回
// VerifyError，绝不删除可能仍被需要的节点。
func (s *Store) Compact() (removed int, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	roots := make([]NodeID, 0, len(s.roots)+1)
	roots = append(roots, s.root)
	for _, r := range s.roots {
		id, perr := ParseNodeID(r.Root)
		if perr != nil {
			return 0, perr
		}
		if !id.IsZero() {
			roots = append(roots, id)
		}
	}

	live := make(map[NodeID]struct{})
	for _, root := range roots {
		if root.IsZero() {
			continue
		}
		if err := s.markReachable(root, live); err != nil {
			return 0, err
		}
	}

	ndir := filepath.Join(s.dir, dirNodes)
	fis, err := ioutil.ReadDir(ndir)
	if err != nil {
		return 0, err
	}
	for _, fi := range fis {
		if !fi.IsDir() {
			continue
		}
		shard := filepath.Join(ndir, fi.Name())
		files, err := ioutil.ReadDir(shard)
		if err != nil {
			return removed, err
		}
		for _, f := range files {
			name := f.Name()
			if f.IsDir() || len(name) != idHexLen {
				continue // 非节点文件（理论上已被启动清扫处理）
			}
			id, perr := ParseNodeID(name)
			if perr != nil {
				continue
			}
			if _, ok := live[id]; ok {
				continue
			}
			if err := os.Remove(filepath.Join(shard, name)); err != nil && !os.IsNotExist(err) {
				return removed, err
			}
			removed++
		}
	}
	if removed > 0 {
		if err := fsyncDir(ndir); err != nil {
			return removed, err
		}
		// 分片目录内的删除也需要 fsync 各分片目录。
		for _, fi := range fis {
			if fi.IsDir() {
				_ = fsyncDir(filepath.Join(ndir, fi.Name()))
			}
		}
	}
	return removed, nil
}

// markReachable 从 root 迭代遍历，标记全部可达节点。
// 损坏引用在此暴露为带路径信息的 VerifyError。
func (s *Store) markReachable(root NodeID, live map[NodeID]struct{}) error {
	stack := []NodeID{root}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if id.IsZero() {
			continue
		}
		if _, ok := live[id]; ok {
			continue
		}
		live[id] = struct{}{}
		// 巡检必须绕过缓存重读磁盘，确保能发现外部损坏/掉字节。
		n, _, err := s.loadNodeFromDisk(id, false)
		if err != nil {
			return err
		}
		for _, cid := range n.children {
			if _, ok := live[cid]; !ok {
				stack = append(stack, cid)
			}
		}
	}
	return nil
}

// Verify 从当前根和全部快照出发做完整引用校验，返回第一个发现的问题；
// 全部有效时返回 nil。用于运维巡检和"损坏引用应定位节点"的验收。
func (s *Store) Verify() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	live := make(map[NodeID]struct{})
	for _, info := range s.Snapshots() {
		if err := s.markReachable(info.Root, live); err != nil {
			return err
		}
	}
	return s.markReachable(s.root, live)
}
