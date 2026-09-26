package gsb

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
)

// 仓库编码布局（全部小端序，字节级真实占用，无任何包装容器）：
//
//	仓库头:  magic[4]="GSB1" | quota i64 | nextID u64 | blockCount u32
//	块记录:  id u64 | refs u32 | dataLen u32 | data[dataLen]        （按 id 升序）
//	分支区:  branchCount u32
//	分支记录: nameLen u16 | name[nameLen] | length u64 | spanCount u32 （按名升序）
//	跨度记录: blockID u64 | off u32 | len u32
//
// EncodedSize() 返回的正是该布局的实际字节数：逻辑序列靠跨度记录共享
// 物理块，重复内容只出现一条块记录，去重收益直接体现在编码体积上。
const (
	storeMagic    = "GSB1"
	repoHeaderLen = 4 + 8 + 8 + 4
	blockRecHead  = 8 + 4 + 4
	branchRecHead = 2 + 8 + 4
	spanRecLen    = 8 + 4 + 4
)

var errBadEncoding = errors.New("gsb: 编码损坏或格式不符")

type encWriter struct{ buf []byte }

func (w *encWriter) u16(v uint16) {
	var b [2]byte
	binary.LittleEndian.PutUint16(b[:], v)
	w.buf = append(w.buf, b[:]...)
}
func (w *encWriter) u32(v uint32) {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], v)
	w.buf = append(w.buf, b[:]...)
}
func (w *encWriter) u64(v uint64) {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], v)
	w.buf = append(w.buf, b[:]...)
}
func (w *encWriter) i64(v int64)  { w.u64(uint64(v)) }
func (w *encWriter) raw(p []byte) { w.buf = append(w.buf, p...) }

// Encode 把仓库序列化为上述布局的字节序列。
func (r *Repo) Encode() []byte {
	w := &encWriter{}
	w.raw([]byte(storeMagic))
	w.i64(int64(r.store.quota))
	w.u64(r.store.nextID)

	ids := make([]uint64, 0, len(r.store.blocks))
	for id := range r.store.blocks {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	w.u32(uint32(len(ids)))
	for _, id := range ids {
		b := r.store.blocks[id]
		w.u64(b.id)
		w.u32(uint32(b.refs))
		w.u32(uint32(len(b.data)))
		w.raw(b.data)
	}

	names := r.Branches()
	sort.Strings(names)
	w.u32(uint32(len(names)))
	for _, name := range names {
		v := r.branches[name]
		w.u16(uint16(len(name)))
		w.raw([]byte(name))
		w.u64(uint64(v.length))
		w.u32(uint32(len(v.spans)))
		for _, s := range v.spans {
			w.u64(s.Block)
			w.u32(uint32(s.Off))
			w.u32(uint32(s.Len))
		}
	}
	return w.buf
}

// EncodedSize 返回当前仓库按编码布局实际占用的字节数。
func (r *Repo) EncodedSize() int { return len(r.Encode()) }

type decReader struct {
	buf []byte
	pos int
}

func (d *decReader) take(n int) ([]byte, error) {
	if n < 0 || d.pos+n > len(d.buf) {
		return nil, errBadEncoding
	}
	p := d.buf[d.pos : d.pos+n]
	d.pos += n
	return p, nil
}

func (d *decReader) u16() (uint16, error) {
	p, err := d.take(2)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint16(p), nil
}

func (d *decReader) u32() (uint32, error) {
	p, err := d.take(4)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(p), nil
}

func (d *decReader) u64() (uint64, error) {
	p, err := d.take(8)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint64(p), nil
}

// Decode 从编码字节重建仓库，恢复物理块、去重索引、引用计数与账本，
// 并在返回前做一次独立账本核对。
func Decode(data []byte) (*Repo, error) {
	d := &decReader{buf: data}
	magic, err := d.take(4)
	if err != nil || string(magic) != storeMagic {
		return nil, errBadEncoding
	}
	quota, err := d.u64()
	if err != nil {
		return nil, err
	}
	nextID, err := d.u64()
	if err != nil {
		return nil, err
	}
	blockCount, err := d.u32()
	if err != nil {
		return nil, err
	}
	store := NewStore(int(quota))
	store.nextID = nextID
	for i := uint32(0); i < blockCount; i++ {
		id, err := d.u64()
		if err != nil {
			return nil, err
		}
		refs, err := d.u32()
		if err != nil {
			return nil, err
		}
		dataLen, err := d.u32()
		if err != nil {
			return nil, err
		}
		if dataLen > BlockSize {
			return nil, fmt.Errorf("%w: 块负载 %d 超过块大小", errBadEncoding, dataLen)
		}
		payload, err := d.take(int(dataLen))
		if err != nil {
			return nil, err
		}
		b := &block{id: id, data: append([]byte(nil), payload...), refs: int(refs)}
		if _, dup := store.blocks[id]; dup {
			return nil, fmt.Errorf("%w: 块 id %d 重复", errBadEncoding, id)
		}
		store.blocks[id] = b
		h := sha256.Sum256(b.data)
		if prev, dup := store.byHash[h]; dup && refs > 0 {
			return nil, fmt.Errorf("%w: 存活块 %d 与 %d 内容重复", errBadEncoding, prev, id)
		}
		store.byHash[h] = id
		size := blockBytes(b)
		store.allocated += size
		if b.refs > 0 {
			store.live += size
		}
	}
	branchCount, err := d.u32()
	if err != nil {
		return nil, err
	}
	r := &Repo{store: store, branches: make(map[string]*Version, branchCount)}
	for i := uint32(0); i < branchCount; i++ {
		nameLen, err := d.u16()
		if err != nil {
			return nil, err
		}
		nameBytes, err := d.take(int(nameLen))
		if err != nil {
			return nil, err
		}
		length, err := d.u64()
		if err != nil {
			return nil, err
		}
		spanCount, err := d.u32()
		if err != nil {
			return nil, err
		}
		v := &Version{length: int(length)}
		for j := uint32(0); j < spanCount; j++ {
			blockID, err := d.u64()
			if err != nil {
				return nil, err
			}
			off, err := d.u32()
			if err != nil {
				return nil, err
			}
			spanLen, err := d.u32()
			if err != nil {
				return nil, err
			}
			v.spans = append(v.spans, Span{Block: blockID, Off: int(off), Len: int(spanLen)})
		}
		r.branches[string(nameBytes)] = v
	}
	if d.pos != len(d.buf) {
		return nil, fmt.Errorf("%w: 尾部多出 %d 字节", errBadEncoding, len(d.buf)-d.pos)
	}
	if err := r.VerifyLedger(); err != nil {
		return nil, fmt.Errorf("%w: 解码后账本核对失败: %v", errBadEncoding, err)
	}
	return r, nil
}
