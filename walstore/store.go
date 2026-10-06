package walstore

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// 页布局(固定长): [pageLSN:8][payload:pagePayloadSize]
// pageLSN = 最后应用到该页的日志记录 LSN + 1(0 表示从未应用),
// 恢复时据此保证重做幂等,同时让合法的 LSN 0 不与初始值混淆。
const pageLSNSize = 8

// Store 是带 WAL 的固定长页存储。
//
// 提交协议(关键不变式):
//  1. 先把事务全部页的物理后像 + commit 记录追加到日志;
//  2. Sync 日志(持久化屏障)——只有此后 Commit 才返回成功;
//  3. 最后才把修改应用到内存页缓存;数据页由 FlushData 惰性落盘。
//
// 因此"提交确认"严格晚于"恢复所需信息落盘"。
type Store struct {
	data     File
	log      *LogWriter
	pageSize int // 含 8 字节 pageLSN 头
	numPages int
	pages    [][]byte // 页缓存,每项长度 = pageSize
	nextTxID uint64
}

func payloadSize(pageSize int) int { return pageSize - pageLSNSize }

// Open 打开(必要时初始化)存储,并执行崩溃恢复。
func Open(dataFile, logFile File, pageSize, numPages int) (*Store, error) {
	if pageSize <= pageLSNSize {
		return nil, errors.New("walstore: pageSize too small")
	}
	s := &Store{
		data:     dataFile,
		log:      NewLogWriter(logFile),
		pageSize: pageSize,
		numPages: numPages,
		pages:    make([][]byte, numPages),
	}
	if dataFile.Size() == 0 { // 全新数据文件:初始化为全零页
		zero := make([]byte, pageSize*numPages)
		if _, err := writeFull(dataFile, zero, 0); err != nil {
			return nil, err
		}
		if err := dataFile.Sync(); err != nil {
			return nil, err
		}
	}
	for i := 0; i < numPages; i++ {
		page := make([]byte, pageSize)
		off := int64(i * pageSize)
		got := 0
		for got < pageSize { // 容忍短读
			n, err := dataFile.ReadAt(page[got:], off+int64(got))
			got += n
			if n == 0 || err != nil {
				break
			}
		}
		s.pages[i] = page
	}
	if err := s.recover(); err != nil {
		return nil, err
	}
	return s, nil
}

func writeFull(f File, p []byte, off int64) (int, error) {
	written := 0
	for written < len(p) {
		n, err := f.WriteAt(p[written:], off+int64(written))
		written += n
		if err != nil {
			return written, err
		}
		if n == 0 {
			return written, errors.New("walstore: write made no progress")
		}
	}
	return written, nil
}

func pageLSN(page []byte) int64 { return int64(binary.BigEndian.Uint64(page[:pageLSNSize])) }

// recover 扫描日志,重做所有已提交事务的页后像。
// 幂等性:仅当记录 LSN 大于页上 pageLSN 时才应用,
// 因此重复恢复(含恢复过程中再次崩溃)结果不变。
func (s *Store) recover() error {
	recs, err := scanLog(s.log.f)
	if err != nil {
		return err
	}
	committed := make(map[uint64]bool)
	var updates []updateRec
	maxTxID := uint64(0)
	for _, r := range recs {
		switch r.typ {
		case recUpdate:
			u, ok := decodeUpdatePayload(r.payload)
			if !ok {
				continue
			}
			updates = append(updates, u)
			if u.txid > maxTxID {
				maxTxID = u.txid
			}
		case recCommit:
			txid, ok := decodeCommitPayload(r.payload)
			if !ok {
				continue
			}
			committed[txid] = true
			if txid > maxTxID {
				maxTxID = txid
			}
		}
	}
	dirty := make(map[uint32]bool)
	for _, u := range updates { // 文件顺序即 LSN 顺序
		if !committed[u.txid] {
			continue // 未提交:全无效(全有或全无)
		}
		if int(u.pageID) >= s.numPages {
			continue
		}
		page := s.pages[u.pageID]
		if u.lsn < pageLSN(page) {
			continue // 已应用过:幂等跳过
		}
		binary.BigEndian.PutUint64(page[:pageLSNSize], uint64(u.lsn+1))
		copy(page[pageLSNSize:], u.payload)
		dirty[u.pageID] = true
	}
	for pageID := range dirty { // 把重做结果写回数据文件
		if _, err := writeFull(s.data, s.pages[pageID], int64(int(pageID)*s.pageSize)); err != nil {
			return err
		}
	}
	if len(dirty) > 0 {
		if err := s.data.Sync(); err != nil {
			return err
		}
	}
	s.nextTxID = maxTxID + 1
	return nil
}

// Begin 开启事务。
func (s *Store) Begin() *Txn {
	tx := &Txn{st: s, txid: s.nextTxID}
	s.nextTxID++
	return tx
}

// Read 返回页载荷的拷贝。
func (s *Store) Read(pageID int) []byte {
	out := make([]byte, payloadSize(s.pageSize))
	copy(out, s.pages[pageID][pageLSNSize:])
	return out
}

// FlushData 把全部页缓存刷到数据文件并持久化(检查点)。
// 原型不截断日志,恢复始终重放全量日志,靠 pageLSN 保持幂等。
func (s *Store) FlushData() error {
	for i, page := range s.pages {
		if _, err := writeFull(s.data, page, int64(i*s.pageSize)); err != nil {
			return err
		}
	}
	return s.data.Sync()
}

// Txn 是一笔事务,暂存对多个固定长页的修改。
type Txn struct {
	st      *Store
	txid    uint64
	writes  []txnWrite
	aborted bool
}

type txnWrite struct {
	pageID  int
	payload []byte
}

// Write 暂存一次整页写入,payload 长度必须等于页载荷长度。
func (t *Txn) Write(pageID int, payload []byte) error {
	if pageID < 0 || pageID >= t.st.numPages {
		return fmt.Errorf("walstore: page %d out of range", pageID)
	}
	if len(payload) != payloadSize(t.st.pageSize) {
		return fmt.Errorf("walstore: payload must be %d bytes", payloadSize(t.st.pageSize))
	}
	cp := make([]byte, len(payload))
	copy(cp, payload)
	t.writes = append(t.writes, txnWrite{pageID: pageID, payload: cp})
	return nil
}

// Commit 提交事务。返回 nil 时,恢复所需信息已全部持久化。
// 中途任何失败都不会应用页缓存,事务等同未发生(全有或全无)。
func (t *Txn) Commit() error {
	st := t.st
	type pending struct {
		w   txnWrite
		lsn int64
	}
	pend := make([]pending, 0, len(t.writes))
	for _, w := range t.writes {
		lsn := st.log.NextLSN()
		got, err := st.log.Append(recUpdate, encodeUpdatePayload(updateRec{
			txid: t.txid, lsn: lsn, pageID: uint32(w.pageID), payload: w.payload,
		}))
		if err != nil {
			return err // 日志尾部留下撕裂记录,恢复时按未提交忽略
		}
		if got != lsn {
			return errors.New("walstore: concurrent log append detected")
		}
		pend = append(pend, pending{w: w, lsn: lsn})
	}
	if _, err := st.log.Append(recCommit, encodeCommitPayload(t.txid)); err != nil {
		return err
	}
	// 持久化屏障:commit 确认必须晚于恢复信息落盘。
	if err := st.log.Sync(); err != nil {
		return err
	}
	for _, p := range pend { // 确认后才应用到页缓存
		page := st.pages[p.w.pageID]
		binary.BigEndian.PutUint64(page[:pageLSNSize], uint64(p.lsn+1))
		copy(page[pageLSNSize:], p.w.payload)
	}
	return nil
}
