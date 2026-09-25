package diskdeque

import (
	"os"
	"path/filepath"
)

// memBlock 是事务工作集中的块。
// id==0 或 dirty==true 的块在提交时分配新编号、写成新文件（copy-on-write），
// 绝不原地覆盖旧块文件；干净的已提交块保持原编号、原文件不动。
type memBlock struct {
	id    uint64
	elems [][]byte
	dirty bool
}

// Tx 表示一个进行中的事务。
//
// 事务内队列的逻辑形态为：
//
//	leftWin ++ 已提交[head:tail] ++ rightWin
//
// leftWin/rightWin 只覆盖被触碰过的两端块，中间块在提交时原样保留，
// 因此两端操作是块级的，不需要整体搬移。
type Tx struct {
	d        *Deque
	leftWin  []memBlock
	rightWin []memBlock
	head     int
	tail     int
	done     bool
}

func (t *Tx) checkState() error {
	switch {
	case t.done:
		return ErrTxDone
	case t.d.crashed:
		return ErrCrashed
	case t.d.closed:
		return ErrClosed
	default:
		return nil
	}
}

// PushLeft 从左端压入。
func (t *Tx) PushLeft(v []byte) error {
	if err := t.checkState(); err != nil {
		return err
	}
	cp := append([]byte(nil), v...)
	if len(t.leftWin) > 0 && len(t.leftWin[0].elems) < t.d.opts.BlockCapacity {
		b := &t.leftWin[0]
		b.elems = append([][]byte{cp}, b.elems...)
		b.dirty = true
	} else {
		t.leftWin = append([]memBlock{{elems: [][]byte{cp}, dirty: true}}, t.leftWin...)
	}
	return nil
}

// PushRight 从右端压入。
func (t *Tx) PushRight(v []byte) error {
	if err := t.checkState(); err != nil {
		return err
	}
	cp := append([]byte(nil), v...)
	if n := len(t.rightWin); n > 0 && len(t.rightWin[n-1].elems) < t.d.opts.BlockCapacity {
		b := &t.rightWin[n-1]
		b.elems = append(b.elems, cp)
		b.dirty = true
	} else {
		t.rightWin = append(t.rightWin, memBlock{elems: [][]byte{cp}, dirty: true})
	}
	return nil
}

func (t *Tx) loadCommittedHead() error {
	id := t.d.m.blocks[t.head]
	elems, err := readBlockFile(t.d.dir, id)
	if err != nil {
		return err
	}
	t.leftWin = append(t.leftWin, memBlock{id: id, elems: elems})
	t.head++
	return nil
}

func (t *Tx) loadCommittedTail() error {
	t.tail--
	id := t.d.m.blocks[t.tail]
	elems, err := readBlockFile(t.d.dir, id)
	if err != nil {
		return err
	}
	t.rightWin = append([]memBlock{{id: id, elems: elems}}, t.rightWin...)
	return nil
}

// PopLeft 从左端弹出；队列为空时返回 ErrEmpty。
func (t *Tx) PopLeft() ([]byte, error) {
	if err := t.checkState(); err != nil {
		return nil, err
	}
	for {
		if len(t.leftWin) > 0 {
			b := &t.leftWin[0]
			if len(b.elems) > 0 {
				v := b.elems[0]
				b.elems = b.elems[1:]
				b.dirty = true
				if len(b.elems) == 0 {
					t.leftWin = t.leftWin[1:]
				}
				return v, nil
			}
			t.leftWin = t.leftWin[1:]
		}
		if t.head < t.tail {
			if err := t.loadCommittedHead(); err != nil {
				return nil, err
			}
			continue
		}
		if len(t.rightWin) > 0 {
			b := &t.rightWin[0]
			if len(b.elems) > 0 {
				v := b.elems[0]
				b.elems = b.elems[1:]
				b.dirty = true
				if len(b.elems) == 0 {
					t.rightWin = t.rightWin[1:]
				}
				return v, nil
			}
			t.rightWin = t.rightWin[1:]
			continue
		}
		return nil, ErrEmpty
	}
}

// PopRight 从右端弹出；队列为空时返回 ErrEmpty。
func (t *Tx) PopRight() ([]byte, error) {
	if err := t.checkState(); err != nil {
		return nil, err
	}
	for {
		if n := len(t.rightWin); n > 0 {
			b := &t.rightWin[n-1]
			if len(b.elems) > 0 {
				v := b.elems[len(b.elems)-1]
				b.elems = b.elems[:len(b.elems)-1]
				b.dirty = true
				if len(b.elems) == 0 {
					t.rightWin = t.rightWin[:n-1]
				}
				return v, nil
			}
			t.rightWin = t.rightWin[:n-1]
		}
		if t.head < t.tail {
			if err := t.loadCommittedTail(); err != nil {
				return nil, err
			}
			continue
		}
		if n := len(t.leftWin); n > 0 {
			b := &t.leftWin[n-1]
			if len(b.elems) > 0 {
				v := b.elems[len(b.elems)-1]
				b.elems = b.elems[:len(b.elems)-1]
				b.dirty = true
				if len(b.elems) == 0 {
					t.leftWin = t.leftWin[:n-1]
				}
				return v, nil
			}
			t.leftWin = t.leftWin[:n-1]
			continue
		}
		return nil, ErrEmpty
	}
}

// Rollback 丢弃事务内的全部修改并释放事务。
func (t *Tx) Rollback() error {
	if t.done {
		return ErrTxDone
	}
	t.done = true
	t.leftWin, t.rightWin = nil, nil
	t.d.mu.Unlock()
	return nil
}

// Commit 原子地提交事务内的两端修改。
func (t *Tx) Commit() error {
	if t.done {
		return ErrTxDone
	}
	t.done = true
	defer t.d.mu.Unlock()
	if t.d.crashed || t.d.closed {
		return ErrCrashed
	}
	return t.d.commit(t)
}

// commit 实现 copy-on-write 的两阶段提交：
//
//  1. 构建新块列表；被修改的块分配全新编号并写入 block_<id>.tmp，fsync
//  2. 写 journal（新块编号 + 新超级块镜像），fsync + rename + 目录 fsync  ← 提交点
//  3. rename 全部块 tmp -> 正式文件，目录 fsync
//  4. tmp+rename 原子替换超级块，目录 fsync
//  5. 删 journal、删除不再被引用的旧块文件，目录 fsync
//
// 提交点之前失败：清理 tmp，事务干净回滚；提交点之后失败：句柄按“崩溃”处理，
// 下次 Open 读取 journal 幂等前滚。
func (d *Deque) commit(t *Tx) error {
	dir := d.dir
	used := make(map[uint64]bool, len(d.m.blocks)+4)
	for _, id := range d.m.blocks {
		used[id] = true
	}

	alloc := d.m.alloc
	allocID := func() (uint64, error) {
		for range d.opts.MaxBlocks {
			id := alloc % d.opts.MaxBlocks
			alloc++
			// 0 是“不存在块”的哨兵编号，不能分配；
			// 活跃块、本事务已分配编号、磁盘上仍存在的文件一律跳过，
			// 因此环绕时绝不会覆盖任何活跃块。
			if id == 0 || used[id] {
				continue
			}
			if fileExists(filepath.Join(dir, blockName(id))) {
				continue
			}
			used[id] = true
			return id, nil
		}
		return 0, ErrBlockSpaceExhausted
	}

	files := make(map[uint64][][]byte)
	var newBlocks []uint64
	var newSizes []int
	emit := func(b memBlock) error {
		if len(b.elems) == 0 {
			return nil
		}
		if !b.dirty && b.id != 0 {
			newBlocks = append(newBlocks, b.id)
			newSizes = append(newSizes, len(b.elems))
			return nil
		}
		id, err := allocID()
		if err != nil {
			return err
		}
		files[id] = b.elems
		newBlocks = append(newBlocks, id)
		newSizes = append(newSizes, len(b.elems))
		return nil
	}

	for _, b := range t.leftWin {
		if err := emit(b); err != nil {
			return err
		}
	}
	for i := t.head; i < t.tail; i++ {
		newBlocks = append(newBlocks, d.m.blocks[i])
		newSizes = append(newSizes, d.sizes[i])
	}
	for _, b := range t.rightWin {
		if err := emit(b); err != nil {
			return err
		}
	}

	var count uint64
	for _, n := range newSizes {
		count += uint64(n)
	}
	nm := meta{txID: d.m.txID + 1, count: count, alloc: alloc, blocks: newBlocks}
	metaImage := nm.marshal()

	// 阶段 1：新块写 tmp 并 fsync
	var written []string
	for id, elems := range files {
		p := filepath.Join(dir, blockTmpName(id))
		if err := writeTmpFile(p, marshalBlock(elems)); err != nil {
			return err
		}
		written = append(written, p)
	}

	// 注入点：提交点之前，可干净回滚
	if err := faultAt(FaultBeforeJournal); err != nil {
		for _, p := range written {
			os.Remove(p)
		}
		fsyncDir(dir)
		return err
	}

	// 阶段 2：journal durable —— 事务提交点
	jdata := (&journal{newBlocks: newBlocks, metaImage: metaImage}).marshal()
	if err := writeFileAtomic(dir, journalFile, jdata); err != nil {
		for _, p := range written {
			os.Remove(p)
		}
		fsyncDir(dir)
		return err
	}

	// 注入点：提交点之后按“进程崩溃”处理，不做任何清理
	if err := faultAt(FaultAfterJournal); err != nil {
		d.crashed = true
		return err
	}

	// 阶段 3：rename 块文件
	for id := range files {
		if err := os.Rename(
			filepath.Join(dir, blockTmpName(id)),
			filepath.Join(dir, blockName(id)),
		); err != nil {
			d.crashed = true
			return err
		}
	}
	if err := fsyncDir(dir); err != nil {
		d.crashed = true
		return err
	}

	if err := faultAt(FaultAfterBlockRename); err != nil {
		d.crashed = true
		return err
	}

	// 阶段 4：原子替换超级块
	if err := writeFileAtomic(dir, metaFile, metaImage); err != nil {
		d.crashed = true
		return err
	}

	if err := faultAt(FaultAfterSuperblock); err != nil {
		d.crashed = true
		return err
	}

	// 阶段 5：清理 journal 与孤儿块
	os.Remove(filepath.Join(dir, journalFile))
	keep := make(map[uint64]bool, len(newBlocks))
	for _, id := range newBlocks {
		keep[id] = true
	}
	for _, old := range d.m.blocks {
		if !keep[old] {
			os.Remove(filepath.Join(dir, blockName(old)))
		}
	}
	fsyncDir(dir)

	d.m = nm
	d.sizes = newSizes
	return nil
}
