package bitmap

import (
	"errors"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Checkpointer 负责位图的检查点归档：
//   - Checkpoint  写全量快照（chk-<gen>.bin）
//   - Incremental 写自上次归档以来的增量（inc-<base>-<gen>.bin），
//     仅包含变更的容器与删除墓碑，开销与变更量成正比。
//
// 归档文件先写临时文件再原子重命名，崩溃不会留下半截文件。
type Checkpointer struct {
	bm      *Bitmap
	dir     string
	lastGen uint64
}

// NewCheckpointer 创建归档器，目录不存在时自动创建。
func NewCheckpointer(bm *Bitmap, dir string) (*Checkpointer, error) {
	if bm == nil {
		return nil, errors.New("bitmap: nil bitmap")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	return &Checkpointer{bm: bm, dir: dir}, nil
}

// LastGen 返回最近一次归档覆盖的代际。
func (c *Checkpointer) LastGen() uint64 { return c.lastGen }

// Checkpoint 写全量检查点，返回文件路径。
// 序列化期间不持锁：通过 pin 强制写线程 COW，停顿仅为索引拷贝。
func (c *Checkpointer) Checkpoint() (string, error) {
	gen, keys, ctrs := c.bm.pinView()

	path := filepath.Join(c.dir, fmt.Sprintf("chk-%020d.bin", gen))
	if err := c.writeAtomic(path, func(f *os.File) error {
		return writeSnapshot(f, snapFull, gen, 0, nil, keys, ctrs)
	}); err != nil {
		c.bm.unpin(gen)
		return "", err
	}
	c.bm.unpin(gen)
	c.lastGen = gen

	// 清理已被检查点覆盖的墓碑，防止无界增长。
	c.bm.mu.Lock()
	kept := c.bm.tombs[:0]
	for _, t := range c.bm.tombs {
		if t.gen > gen {
			kept = append(kept, t)
		}
	}
	c.bm.tombs = kept
	c.bm.mu.Unlock()
	return path, nil
}

// Incremental 写增量快照（自上次归档以来的变更）。
// 若期间无任何变更，不写文件并返回 ("", nil)。
func (c *Checkpointer) Incremental() (string, error) {
	base := c.lastGen
	gen, tombs, keys, ctrs := c.bm.pinDelta(base)
	pinGen := gen

	if len(tombs) == 0 && len(keys) == 0 {
		c.bm.unpin(pinGen)
		return "", nil // 无变更
	}
	path := filepath.Join(c.dir, fmt.Sprintf("inc-%020d-%020d.bin", base, gen))
	if err := c.writeAtomic(path, func(f *os.File) error {
		return writeSnapshot(f, snapIncr, gen, base, tombs, keys, ctrs)
	}); err != nil {
		c.bm.unpin(pinGen)
		return "", err
	}
	c.bm.unpin(pinGen)
	c.lastGen = gen
	return path, nil
}

func (c *Checkpointer) writeAtomic(path string, fn func(*os.File) error) error {
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if err := fn(f); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// Restore 从归档目录恢复位图：
// 加载最新全量检查点，然后按代际链依次应用增量快照。
func Restore(dir string) (*Bitmap, error) {
	entries, err := ioutil.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	type incRef struct {
		path string
		base uint64
		gen  uint64
	}
	var chkPath string
	var chkGen uint64
	haveChk := false
	incs := make(map[uint64]incRef)
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "chk-") && strings.HasSuffix(name, ".bin") {
			g, err := strconv.ParseUint(name[4:len(name)-4], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("bitmap: bad checkpoint name %q", name)
			}
			if !haveChk || g > chkGen {
				haveChk, chkGen = true, g
				chkPath = filepath.Join(dir, name)
			}
		} else if strings.HasPrefix(name, "inc-") && strings.HasSuffix(name, ".bin") {
			parts := strings.Split(name[4:len(name)-4], "-")
			if len(parts) != 2 {
				return nil, fmt.Errorf("bitmap: bad incremental name %q", name)
			}
			base, err1 := strconv.ParseUint(parts[0], 10, 64)
			g, err2 := strconv.ParseUint(parts[1], 10, 64)
			if err1 != nil || err2 != nil {
				return nil, fmt.Errorf("bitmap: bad incremental name %q", name)
			}
			if old, dup := incs[base]; dup && old.gen != g {
				return nil, fmt.Errorf("bitmap: ambiguous incrementals for base %d", base)
			}
			incs[base] = incRef{path: filepath.Join(dir, name), base: base, gen: g}
		}
	}
	if !haveChk {
		return nil, errors.New("bitmap: no checkpoint found in " + dir)
	}

	bm := New()
	if err := applyFull(chkPath, bm); err != nil {
		return nil, err
	}
	// 沿代际链应用增量。
	for {
		inc, ok := incs[bm.gen]
		if !ok || inc.gen <= bm.gen {
			break
		}
		if err := applyIncremental(inc.path, bm); err != nil {
			return nil, err
		}
	}
	return bm, nil
}

func applyFull(path string, bm *Bitmap) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	hdr, _, keys, ctrs, err := readSnapshot(f)
	if err != nil {
		return err
	}
	if hdr.typ != snapFull {
		return fmt.Errorf("bitmap: %s is not a full checkpoint", path)
	}
	bm.keys = keys
	bm.ctrs = ctrs
	bm.gens = make([]uint64, len(keys))
	for i := range bm.gens {
		bm.gens[i] = hdr.gen
	}
	bm.gen = hdr.gen
	return nil
}

func applyIncremental(path string, bm *Bitmap) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	hdr, tombs, keys, ctrs, err := readSnapshot(f)
	if err != nil {
		return err
	}
	if hdr.typ != snapIncr {
		return fmt.Errorf("bitmap: %s is not an incremental snapshot", path)
	}
	if hdr.base != bm.gen {
		return fmt.Errorf("bitmap: incremental base %d does not match bitmap gen %d", hdr.base, bm.gen)
	}
	// 先应用删除墓碑，再 upsert 变更容器。
	for _, t := range tombs {
		if i := bm.find(t); i >= 0 {
			bm.removeAt(i, false)
		}
	}
	for j, k := range keys {
		i := bm.find(k)
		if i >= 0 {
			bm.ctrs[i] = ctrs[j]
			bm.gens[i] = hdr.gen
		} else {
			bm.gen = hdr.gen // insertAt 使用 bm.gen 作为槽位代际
			bm.insertAt(-i-1, k, ctrs[j])
			bm.gens[-i-1] = hdr.gen
		}
	}
	bm.gen = hdr.gen
	return nil
}
