package main

import (
	"bufio"
	"container/heap"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"os"
)

// countWriter 统计流过的字节数，用于推算运行段游标。
type countWriter struct{ n *int64 }

func (w countWriter) Write(p []byte) (int, error) {
	*w.n += int64(len(p))
	return len(p), nil
}

// runReader 包装一个运行段文件；offset() 给出下一条未消费记录的字节偏移，
// 即使 bufio 预读了数据也能给出精确位置。
type runReader struct {
	f   *os.File
	br  *bufio.Reader
	cnt int64
}

func (rr *runReader) offset() int64 {
	return rr.cnt - int64(rr.br.Buffered())
}

type heapItem struct {
	rec record
	run int
}

// mergeHeap 按 (key, seq) 排序：键相同按全局输入序号，保证稳定。
type mergeHeap []heapItem

func (h mergeHeap) Len() int { return len(h) }
func (h mergeHeap) Less(i, j int) bool {
	if h[i].rec.key != h[j].rec.key {
		return h[i].rec.key < h[j].rec.key
	}
	return h[i].rec.seq < h[j].rec.seq
}
func (h mergeHeap) Swap(i, j int)       { h[i], h[j] = h[j], h[i] }
func (h *mergeHeap) Push(x interface{}) { *h = append(*h, x.(heapItem)) }
func (h *mergeHeap) Pop() interface{} {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

// merger 执行 k 路归并，并按检查点协议发布进度。
type merger struct {
	dir             string
	ck              *checkpoint
	readers         []*runReader
	heap            mergeHeap
	heapStart       []int64 // 各段当前在堆中记录的起始偏移
	inHeap          []bool  // 各段当前是否有一条记录在堆中
	outF            *os.File
	outW            *bufio.Writer
	outHash         hash.Hash // 覆盖所有已落盘输出字节
	emitted         uint64
	emittedThisRun  uint64
	checkpointEvery uint64
	failAfter       uint64 // 本次尝试发出 N 条后模拟崩溃；0 表示不注入
}

// newMerger 从检查点恢复（检查点必须已存在）。
// 恢复时校验：各输入段摘要、已发布输出前缀摘要；
// 输出截断到已发布前缀，未同步的尾部一律丢弃。
func newMerger(dir string, ckEvery, failAfter uint64) (*merger, error) {
	ck, err := loadCheckpoint(dir)
	if err != nil {
		return nil, err
	}
	m := &merger{
		dir:             dir,
		ck:              ck,
		heapStart:       make([]int64, len(ck.Runs)),
		inHeap:          make([]bool, len(ck.Runs)),
		checkpointEvery: ckEvery,
		failAfter:       failAfter,
		emitted:         ck.Emitted,
	}
	if len(ck.Cursors) != len(ck.Runs) {
		return nil, fmt.Errorf("checkpoint: %d cursors for %d runs", len(ck.Cursors), len(ck.Runs))
	}

	// 1. 校验并打开所有输入段，seek 到游标。
	m.readers = make([]*runReader, len(ck.Runs))
	for i, ri := range ck.Runs {
		if err := verifyRun(ri); err != nil {
			m.Close()
			return nil, err
		}
		f, err := openTracked(ri.Path, os.O_RDONLY, 0)
		if err != nil {
			m.Close()
			return nil, err
		}
		off := ck.Cursors[i]
		if off < 0 || off > ri.Size {
			m.Close()
			return nil, fmt.Errorf("run %s: cursor %d out of range [0,%d]", ri.Path, off, ri.Size)
		}
		if _, err := f.Seek(off, io.SeekStart); err != nil {
			m.Close()
			return nil, err
		}
		rr := &runReader{f: f, cnt: off}
		rr.br = bufio.NewReader(io.TeeReader(f, countWriter{&rr.cnt}))
		m.readers[i] = rr
	}

	// 2. 校验已发布输出前缀，截断到前缀长度（丢弃未同步缓冲的残骸）。
	outF, err := openTracked(ck.OutPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		m.Close()
		return nil, err
	}
	st, err := outF.Stat()
	if err != nil {
		m.Close()
		return nil, err
	}
	if st.Size() < ck.OutLen {
		m.Close()
		return nil, fmt.Errorf("output %s: size %d < published prefix %d", ck.OutPath, st.Size(), ck.OutLen)
	}
	h := sha256.New()
	if _, err := io.CopyN(h, outF, ck.OutLen); err != nil {
		m.Close()
		return nil, err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != ck.OutSHA {
		m.Close()
		return nil, fmt.Errorf("output %s: published prefix digest mismatch", ck.OutPath)
	}
	if err := outF.Truncate(ck.OutLen); err != nil {
		m.Close()
		return nil, err
	}
	if _, err := outF.Seek(ck.OutLen, io.SeekStart); err != nil {
		m.Close()
		return nil, err
	}
	m.outF = outF
	m.outHash = h // h 已是前缀摘要，继续流式追加
	m.outW = bufio.NewWriter(io.MultiWriter(outF, h))

	// 3. 预读各段当前记录，建堆。
	for i, rr := range m.readers {
		if rr.offset() >= ck.Runs[i].Size {
			continue
		}
		start := rr.offset()
		rec, err := readRecord(rr.br)
		if err != nil {
			m.Close()
			return nil, fmt.Errorf("run %s: %v", ck.Runs[i].Path, err)
		}
		m.heapStart[i] = start
		m.inHeap[i] = true
		heap.Push(&m.heap, heapItem{rec: rec, run: i})
	}
	return m, nil
}

// run 执行归并直到完成；注入故障时通过 crashFunc 中断（缓冲区随之丢失）。
func (m *merger) run() error {
	for m.heap.Len() > 0 {
		it := heap.Pop(&m.heap).(heapItem)
		m.inHeap[it.run] = false
		if err := writeRecord(m.outW, it.rec); err != nil {
			return err
		}
		m.emitted++
		m.emittedThisRun++
		if m.failAfter > 0 && m.emittedThisRun >= m.failAfter {
			crashFunc() // 模拟崩溃：不 flush、不关闭
		}
		rr := m.readers[it.run]
		if rr.offset() < m.ck.Runs[it.run].Size {
			start := rr.offset()
			rec, err := readRecord(rr.br)
			if err != nil {
				return err
			}
			m.heapStart[it.run] = start
			m.inHeap[it.run] = true
			heap.Push(&m.heap, heapItem{rec: rec, run: it.run})
		}
		if m.checkpointEvery > 0 && m.emitted%m.checkpointEvery == 0 {
			if err := m.publish(); err != nil {
				return err
			}
		}
	}
	return m.publish() // 收尾：最终 flush+fsync 并提交检查点
}

// publish 发布进度：先 flush+fsync 输出，再原子提交检查点。
// 顺序保证检查点引用的输出前缀一定已持久化。
func (m *merger) publish() error {
	if err := m.outW.Flush(); err != nil {
		return err
	}
	if err := m.outF.Sync(); err != nil {
		return err
	}
	st, err := m.outF.Stat()
	if err != nil {
		return err
	}
	m.ck.OutLen = st.Size()
	m.ck.OutSHA = hex.EncodeToString(m.outHash.Sum(nil))
	m.ck.Emitted = m.emitted
	for i, rr := range m.readers {
		// 堆中已预读但未发布的记录不计入游标：
		// 恢复时会重新读入并重新入堆。
		if m.inHeap[i] {
			m.ck.Cursors[i] = m.heapStart[i]
		} else {
			m.ck.Cursors[i] = rr.offset()
		}
	}
	return saveCheckpoint(m.dir, m.ck)
}

// totalRecords 返回所有运行段的记录总数。
func (m *merger) totalRecords() int {
	total := 0
	for _, ri := range m.ck.Runs {
		total += ri.Count
	}
	return total
}

func (m *merger) Close() {
	for _, rr := range m.readers {
		if rr != nil && rr.f != nil {
			closeTracked(rr.f)
		}
	}
	if m.outF != nil {
		closeTracked(m.outF)
	}
}
