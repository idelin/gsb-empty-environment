// Package volmap 实现逻辑偏移到不可变物理块的区间映射。
//
// 数据模型：一个 Volume 是某个目录下的一组文件。
//   - blk-<id>        不可变物理块，写入后永不修改（写即复制）。
//   - manifest.json   清单，记录活跃视图的区间映射、全部快照和分配水位。
//
// 所有修改（Write/PunchHole/Snapshot）都先落物理块，再以
// “写临时文件 + fsync + rename + fsync 目录” 的方式原子提交清单。
// 洞不占用物理块：打洞只是从区间映射中移除对应区间，读洞返回零。
package volmap

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

const (
	manifestFile = "manifest.json"
	manifestTmp  = "manifest.json.tmp"
)

// manifest 是持久化的全部元数据。
type manifest struct {
	NextBlock uint64                `json:"nextBlock"`
	NextSnap  uint64                `json:"nextSnap"`
	Live      []Interval            `json:"live"`
	Snaps     map[uint64][]Interval `json:"snaps"`
}

// clone 返回清单的写时复制副本：活跃区间切片独立，快照区间只读共享。
func (m manifest) clone() manifest {
	out := m
	out.Live = append([]Interval(nil), m.Live...)
	out.Snaps = make(map[uint64][]Interval, len(m.Snaps))
	for k, ivs := range m.Snaps {
		out.Snaps[k] = ivs
	}
	return out
}

// Volume 是一个位于指定目录下的卷。
type Volume struct {
	dir  string
	hook FaultHook

	mu sync.Mutex
	m  manifest
}

// Open 打开（必要时创建）dir 下的卷，并执行崩溃恢复：
// 丢弃未完成的清单临时文件，回收不被清单引用的孤儿物理块。
// hook 可为 nil；非 nil 时在故障注入点被回调。
func Open(dir string, hook FaultHook) (*Volume, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	v := &Volume{dir: dir, hook: hook}

	// 恢复 1：上次崩溃可能留下未 rename 的临时清单，直接丢弃。
	// rename 的原子性保证 manifest.json 要么是旧的完整版本，要么是新的。
	if err := os.Remove(filepath.Join(dir, manifestTmp)); err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	data, err := ioutil.ReadFile(filepath.Join(dir, manifestFile))
	switch {
	case os.IsNotExist(err):
		v.m = manifest{Snaps: map[uint64][]Interval{}}
	case err != nil:
		return nil, err
	default:
		if err := json.Unmarshal(data, &v.m); err != nil {
			return nil, fmt.Errorf("volmap: 清单损坏: %v", err)
		}
		if v.m.Snaps == nil {
			v.m.Snaps = map[uint64][]Interval{}
		}
	}

	// 恢复 2：回收孤儿块（块已写盘但清单提交前崩溃）和残留的块临时文件。
	if err := v.gcOrphanBlocks(); err != nil {
		return nil, err
	}
	return v, nil
}

// Write 把 data 写到逻辑偏移 off 处。只修改 [off, off+len(data)) 覆盖区：
// 被打到的旧区间在边界分裂，覆盖区之外的字节保持不变。
// 数据先写入一个全新的不可变物理块，再原子提交新区间映射。
func (v *Volume) Write(off uint64, data []byte) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if len(data) == 0 {
		return nil
	}
	id := v.m.NextBlock
	if err := v.writeBlock(id, data); err != nil {
		return err
	}
	if err := v.runHook(FaultAfterBlockSync); err != nil {
		return err
	}
	end := off + uint64(len(data))
	nm := v.m.clone()
	nm.NextBlock++
	nm.Live = insert(carve(nm.Live, off, end), Interval{Start: off, End: end, Block: id, BOff: 0})
	return v.commitLocked(nm)
}

// PunchHole 在 [off, off+length) 打洞：移除覆盖的区间（边界分裂、
// 两侧保留），洞内读为零，且不再占用任何物理块。
func (v *Volume) PunchHole(off, length uint64) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if length == 0 {
		return nil
	}
	nm := v.m.clone()
	nm.Live = carve(nm.Live, off, off+length)
	return v.commitLocked(nm)
}

// Snapshot 对当前活跃视图打快照，返回快照 ID。
// 快照是区间映射的副本；之后的写入/打洞只改活跃视图，
// 快照引用的物理块因引用计数保留而不会被回收。
func (v *Volume) Snapshot() (uint64, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	id := v.m.NextSnap
	nm := v.m.clone()
	nm.NextSnap++
	nm.Snaps[id] = append([]Interval(nil), v.m.Live...)
	if err := v.commitLocked(nm); err != nil {
		return 0, err
	}
	return id, nil
}

// Read 从活跃视图的 off 处读满 buf。洞和未写入区域读为零。
func (v *Volume) Read(buf []byte, off uint64) (int, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.readFrom(v.m.Live, buf, off)
}

// ReadSnapshot 从指定快照视图的 off 处读满 buf。
func (v *Volume) ReadSnapshot(id uint64, buf []byte, off uint64) (int, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	ivs, ok := v.m.Snaps[id]
	if !ok {
		return 0, fmt.Errorf("volmap: 快照 %d 不存在", id)
	}
	return v.readFrom(ivs, buf, off)
}

// readFrom 按区间映射读数据，未覆盖区域保持为零。
func (v *Volume) readFrom(ivs []Interval, buf []byte, off uint64) (int, error) {
	for i := range buf {
		buf[i] = 0
	}
	end := off + uint64(len(buf))
	for _, iv := range ivs {
		if iv.End <= off {
			continue
		}
		if iv.Start >= end {
			break
		}
		lo, hi := iv.Start, iv.End
		if lo < off {
			lo = off
		}
		if hi > end {
			hi = end
		}
		f, err := os.Open(filepath.Join(v.dir, blockName(iv.Block)))
		if err != nil {
			return 0, err
		}
		_, err = f.ReadAt(buf[lo-off:hi-off], int64(iv.BOff+(lo-iv.Start)))
		f.Close()
		if err != nil {
			return 0, err
		}
	}
	return len(buf), nil
}

// writeBlock 把 data 落成一个不可变物理块：临时文件 + fsync + rename + fsync 目录。
func (v *Volume) writeBlock(id uint64, data []byte) error {
	final := filepath.Join(v.dir, blockName(id))
	tmp := final + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
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
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, final); err != nil {
		return err
	}
	return syncDir(v.dir)
}

// commitLocked 原子提交清单 nm：临时文件 + fsync + rename + fsync 目录。
// 只有提交成功，内存状态才切换为 nm；随后回收不再被引用的物理块。
func (v *Volume) commitLocked(nm manifest) error {
	data, err := json.MarshalIndent(&nm, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(v.dir, manifestTmp)
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
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
	if err := f.Close(); err != nil {
		return err
	}
	if err := v.runHook(FaultBeforeManifestRename); err != nil {
		return err
	}
	if err := os.Rename(tmp, filepath.Join(v.dir, manifestFile)); err != nil {
		return err
	}
	if err := v.runHook(FaultAfterManifestRename); err != nil {
		return err
	}
	if err := syncDir(v.dir); err != nil {
		return err
	}
	v.m = nm
	// 清单已提交，此时删除失去引用的块是安全的：
	// 即使删到一半崩溃，Open 时的孤儿回收会收尾。
	v.gcOrphanBlocks()
	return nil
}

// referencedBlocks 返回活跃视图与所有快照共同引用的块集合。
func (v *Volume) referencedBlocks() map[uint64]bool {
	ref := map[uint64]bool{}
	for _, iv := range v.m.Live {
		ref[iv.Block] = true
	}
	for _, ivs := range v.m.Snaps {
		for _, iv := range ivs {
			ref[iv.Block] = true
		}
	}
	return ref
}

// gcOrphanBlocks 删除不被清单引用的块文件和残留的块临时文件。
func (v *Volume) gcOrphanBlocks() error {
	ref := v.referencedBlocks()
	names, err := blockFileNames(v.dir)
	if err != nil {
		return err
	}
	for _, name := range names {
		if strings.HasSuffix(name, ".tmp") {
			os.Remove(filepath.Join(v.dir, name))
			continue
		}
		id, ok := parseBlockName(name)
		if !ok {
			continue
		}
		if !ref[id] {
			if err := os.Remove(filepath.Join(v.dir, name)); err != nil {
				return err
			}
		}
	}
	return nil
}

func (v *Volume) runHook(p FaultPoint) error {
	if v.hook == nil {
		return nil
	}
	return v.hook(p)
}

func blockName(id uint64) string { return fmt.Sprintf("blk-%016x", id) }

func parseBlockName(name string) (uint64, bool) {
	if !strings.HasPrefix(name, "blk-") {
		return 0, false
	}
	var id uint64
	if _, err := fmt.Sscanf(name, "blk-%016x", &id); err != nil {
		return 0, false
	}
	return id, true
}

func blockFileNames(dir string) ([]string, error) {
	fis, err := ioutil.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, fi := range fis {
		if strings.HasPrefix(fi.Name(), "blk-") {
			names = append(names, fi.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
