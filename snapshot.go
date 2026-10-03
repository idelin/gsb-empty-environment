package bitmap

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"
)

const (
	snapMagic   uint32 = 0x52424D31 // "RBM1"
	snapVersion byte   = 1
)

const (
	snapFull byte = 0 // 全量检查点
	snapIncr byte = 1 // 增量快照
)

// Snapshot 是位图在某一时刻的不可变视图。
// 创建代价为 O(容器数)（仅复制索引切片，容器共享）；
// 此后对原位图的写入通过 COW 隔离，绝不影响快照。
// 使用完毕必须调用 Release 以解除原位图的 COW 约束。
type Snapshot struct {
	bm   *Bitmap
	gen  uint64
	keys []uint16
	ctrs []container
	once sync.Once
}

// Snapshot 创建当前位图的轻量级不可变快照。
func (b *Bitmap) Snapshot() *Snapshot {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.gen++
	keys := make([]uint16, len(b.keys))
	copy(keys, b.keys)
	ctrs := make([]container, len(b.ctrs))
	copy(ctrs, b.ctrs)
	s := &Snapshot{bm: b, gen: b.gen, keys: keys, ctrs: ctrs}
	if b.snaps == nil {
		b.snaps = make(map[uint64]int)
	}
	b.snaps[b.gen]++
	b.maxSnap = b.gen // gen 单调递增，新快照即最大
	return s
}

// Gen 返回快照对应的位图代际。
func (s *Snapshot) Gen() uint64 { return s.gen }

// Contains 判断值是否存在于快照中。
func (s *Snapshot) Contains(x uint32) bool {
	k, v := uint16(x>>16), uint16(x)
	lo, hi := 0, len(s.keys)
	for lo < hi {
		mid := (lo + hi) / 2
		if s.keys[mid] < k {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo < len(s.keys) && s.keys[lo] == k && s.ctrs[lo].contains(v)
}

// Cardinality 返回快照中的元素总数。
func (s *Snapshot) Cardinality() uint64 {
	var n uint64
	for _, c := range s.ctrs {
		n += uint64(c.cardinality())
	}
	return n
}

// Iterator 创建快照的迭代器（并发安全，快照不可变）。
func (s *Snapshot) Iterator() *Iterator {
	return &Iterator{keys: s.keys, ctrs: s.ctrs, ki: -1}
}

// Release 释放快照。幂等；释放全部快照后原位图恢复就地写入（无 COW 开销）。
func (s *Snapshot) Release() {
	s.once.Do(func() {
		b := s.bm
		b.mu.Lock()
		defer b.mu.Unlock()
		if n := b.snaps[s.gen]; n > 1 {
			b.snaps[s.gen] = n - 1
		} else {
			delete(b.snaps, s.gen)
		}
		b.maxSnap = 0
		for g := range b.snaps {
			if g > b.maxSnap {
				b.maxSnap = g
			}
		}
	})
}

// ---------------- 序列化 ----------------

type snapHeader struct {
	typ    byte
	gen    uint64
	base   uint64
	nTombs uint32
	nCtrs  uint32
}

// writeSnapshot 将（全量或增量）快照写入 w。
func writeSnapshot(w io.Writer, typ byte, gen, base uint64, tombs []uint16, keys []uint16, ctrs []container) error {
	bw := bufio.NewWriter(w)
	if err := binary.Write(bw, binary.LittleEndian, snapMagic); err != nil {
		return err
	}
	if err := binary.Write(bw, binary.LittleEndian, snapVersion); err != nil {
		return err
	}
	if err := binary.Write(bw, binary.LittleEndian, typ); err != nil {
		return err
	}
	for _, v := range []uint64{gen, base, uint64(len(tombs)), uint64(len(keys))} {
		if err := binary.Write(bw, binary.LittleEndian, v); err != nil {
			return err
		}
	}
	for _, t := range tombs {
		if err := binary.Write(bw, binary.LittleEndian, t); err != nil {
			return err
		}
	}
	for i, k := range keys {
		if err := writeContainer(bw, k, ctrs[i]); err != nil {
			return err
		}
	}
	return bw.Flush()
}

func writeContainer(w io.Writer, key uint16, c container) error {
	if err := binary.Write(w, binary.LittleEndian, key); err != nil {
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, c.ctype()); err != nil {
		return err
	}
	if err := binary.Write(w, binary.LittleEndian, uint32(c.cardinality())); err != nil {
		return err
	}
	switch t := c.(type) {
	case *arrayContainer:
		return binary.Write(w, binary.LittleEndian, t.data)
	case *bitmapContainer:
		return binary.Write(w, binary.LittleEndian, t.words[:])
	case *runContainer:
		if err := binary.Write(w, binary.LittleEndian, uint32(len(t.ivs))); err != nil {
			return err
		}
		for _, iv := range t.ivs {
			if err := binary.Write(w, binary.LittleEndian, iv.start); err != nil {
				return err
			}
			if err := binary.Write(w, binary.LittleEndian, iv.last); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("unknown container type %T", c)
	}
}

// readSnapshot 读取并校验一个快照文件。
func readSnapshot(r io.Reader) (snapHeader, []uint16, []uint16, []container, error) {
	var hdr snapHeader
	br := bufio.NewReader(r)
	var magic uint32
	var version byte
	if err := binary.Read(br, binary.LittleEndian, &magic); err != nil {
		return hdr, nil, nil, nil, err
	}
	if magic != snapMagic {
		return hdr, nil, nil, nil, errors.New("bitmap: bad magic")
	}
	if err := binary.Read(br, binary.LittleEndian, &version); err != nil {
		return hdr, nil, nil, nil, err
	}
	if version != snapVersion {
		return hdr, nil, nil, nil, fmt.Errorf("bitmap: unsupported version %d", version)
	}
	if err := binary.Read(br, binary.LittleEndian, &hdr.typ); err != nil {
		return hdr, nil, nil, nil, err
	}
	var fields [4]uint64
	for i := range fields {
		if err := binary.Read(br, binary.LittleEndian, &fields[i]); err != nil {
			return hdr, nil, nil, nil, err
		}
	}
	hdr.gen, hdr.base = fields[0], fields[1]
	if fields[2] > 1<<16 || fields[3] > 1<<16 {
		return hdr, nil, nil, nil, errors.New("bitmap: corrupt snapshot (count overflow)")
	}
	hdr.nTombs, hdr.nCtrs = uint32(fields[2]), uint32(fields[3])

	tombs := make([]uint16, 0, hdr.nTombs)
	for i := uint32(0); i < hdr.nTombs; i++ {
		var t uint16
		if err := binary.Read(br, binary.LittleEndian, &t); err != nil {
			return hdr, nil, nil, nil, err
		}
		tombs = append(tombs, t)
	}
	keys := make([]uint16, 0, hdr.nCtrs)
	ctrs := make([]container, 0, hdr.nCtrs)
	for i := uint32(0); i < hdr.nCtrs; i++ {
		k, c, err := readContainer(br)
		if err != nil {
			return hdr, nil, nil, nil, err
		}
		keys = append(keys, k)
		ctrs = append(ctrs, c)
	}
	return hdr, tombs, keys, ctrs, nil
}

func readContainer(r io.Reader) (uint16, container, error) {
	var key uint16
	var ctype byte
	var card uint32
	if err := binary.Read(r, binary.LittleEndian, &key); err != nil {
		return 0, nil, err
	}
	if err := binary.Read(r, binary.LittleEndian, &ctype); err != nil {
		return 0, nil, err
	}
	if err := binary.Read(r, binary.LittleEndian, &card); err != nil {
		return 0, nil, err
	}
	if card > 1<<16 {
		return 0, nil, errors.New("bitmap: corrupt container cardinality")
	}
	switch ctype {
	case typeArray:
		data := make([]uint16, card)
		if err := binary.Read(r, binary.LittleEndian, &data); err != nil {
			return 0, nil, err
		}
		return key, &arrayContainer{data: data}, nil
	case typeBitmap:
		b := &bitmapContainer{card: int(card)}
		if err := binary.Read(r, binary.LittleEndian, b.words[:]); err != nil {
			return 0, nil, err
		}
		return key, b, nil
	case typeRun:
		var nRuns uint32
		if err := binary.Read(r, binary.LittleEndian, &nRuns); err != nil {
			return 0, nil, err
		}
		if nRuns > 1<<15 {
			return 0, nil, errors.New("bitmap: corrupt run count")
		}
		ivs := make([]interval, nRuns)
		for i := range ivs {
			if err := binary.Read(r, binary.LittleEndian, &ivs[i].start); err != nil {
				return 0, nil, err
			}
			if err := binary.Read(r, binary.LittleEndian, &ivs[i].last); err != nil {
				return 0, nil, err
			}
		}
		return key, &runContainer{ivs: ivs}, nil
	default:
		return 0, nil, fmt.Errorf("bitmap: unknown container type %d", ctype)
	}
}

// Save 将位图的全量快照写入 w（持读锁，不阻塞其他读者）。
func (b *Bitmap) Save(w io.Writer) error {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return writeSnapshot(w, snapFull, b.gen, 0, nil, b.keys, b.ctrs)
}

// ReadBitmap 从 r 读取全量快照并构建新位图。
func ReadBitmap(r io.Reader) (*Bitmap, error) {
	hdr, _, keys, ctrs, err := readSnapshot(r)
	if err != nil {
		return nil, err
	}
	if hdr.typ != snapFull {
		return nil, errors.New("bitmap: not a full snapshot")
	}
	b := &Bitmap{keys: keys, ctrs: ctrs, gen: hdr.gen}
	b.gens = make([]uint64, len(keys))
	for i := range b.gens {
		b.gens[i] = hdr.gen
	}
	return b, nil
}
