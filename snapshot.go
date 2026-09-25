package diskqueue

import (
	"container/heap"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"os"
	"path/filepath"
)

const snapshotMagic = "DQS1"

var errCorruptSnapshot = errors.New("diskqueue: corrupt snapshot")

// 快照格式: magic(4) | nextSeq(8) | count(4) | [seq(8) prio(8) payloadLen(4) payload]... | crc32(4)
// 写入采用 tmp + rename，保证快照文件要么完整旧版、要么完整新版，不会出现半写状态。

func (q *Queue) writeSnapshot() error {
	var buf []byte
	buf = append(buf, snapshotMagic...)
	buf = binary.LittleEndian.AppendUint64(buf, q.nextSeq)
	buf = binary.LittleEndian.AppendUint32(buf, uint32(len(q.h)))
	for _, e := range q.h {
		buf = binary.LittleEndian.AppendUint64(buf, e.seq)
		buf = binary.LittleEndian.AppendUint64(buf, uint64(e.prio))
		buf = binary.LittleEndian.AppendUint32(buf, uint32(len(e.payload)))
		buf = append(buf, e.payload...)
	}
	crc := crc32.ChecksumIEEE(buf)
	buf = binary.LittleEndian.AppendUint32(buf, crc)

	tmp := filepath.Join(q.dir, "snapshot.tmp")
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(buf); err != nil {
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
	if err := os.Rename(tmp, filepath.Join(q.dir, "snapshot")); err != nil {
		return err
	}
	return syncDir(q.dir)
}

func (q *Queue) loadSnapshot(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(data) < 4+8+4+4 || string(data[:4]) != snapshotMagic {
		return errCorruptSnapshot
	}
	body := data[:len(data)-4]
	if crc32.ChecksumIEEE(body) != binary.LittleEndian.Uint32(data[len(data)-4:]) {
		return errCorruptSnapshot
	}
	r := &byteReader{b: body[4:]}
	q.nextSeq = r.u64()
	q.baseSeq = q.nextSeq
	count := r.u32()
	for i := uint32(0); i < count; i++ {
		seq := r.u64()
		prio := int64(r.u64())
		payload := r.bytes(r.u32())
		if r.failed {
			return errCorruptSnapshot
		}
		e := &entry{seq: seq, prio: prio, payload: payload}
		q.h = append(q.h, e)
		q.bySeq[seq] = e
	}
	heap.Init(&q.h)
	return nil
}

type byteReader struct {
	b      []byte
	off    int
	failed bool
}

func (r *byteReader) u64() uint64 {
	if r.failed || len(r.b)-r.off < 8 {
		r.failed = true
		return 0
	}
	v := binary.LittleEndian.Uint64(r.b[r.off:])
	r.off += 8
	return v
}

func (r *byteReader) u32() uint32 {
	if r.failed || len(r.b)-r.off < 4 {
		r.failed = true
		return 0
	}
	v := binary.LittleEndian.Uint32(r.b[r.off:])
	r.off += 4
	return v
}

func (r *byteReader) bytes(n uint32) []byte {
	if r.failed || uint64(len(r.b)-r.off) < uint64(n) {
		r.failed = true
		return nil
	}
	v := append([]byte(nil), r.b[r.off:r.off+int(n)]...)
	r.off += int(n)
	return v
}

func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
