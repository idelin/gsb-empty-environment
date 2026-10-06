package walstore

import (
	"encoding/binary"
	"fmt"
)

const (
	// PageSize 是磁盘页大小（固定长），前 8 字节为 pageLSN。
	PageSize     = 64
	pageLSNSize  = 8
	PageDataSize = PageSize - pageLSNSize
)

// Store 是带 WAL 的固定长页存储（redo-only、no-steal 原型）。
//
// 提交协议：
//  1. 把事务的页镜像记录 + commit 记录追加到 WAL；
//  2. Sync WAL —— 提交点。确认（ack）必须晚于这一步，
//     即恢复所需的全部信息落盘之后才允许向外界确认；
//  3. 把脏页写入页文件（刻意不 Sync；丢失则由恢复重做）。
//
// 恢复协议：
//  扫描 WAL 有效前缀，按 LSN 顺序重做已提交事务的页镜像；
//  页头 pageLSN 使重做幂等（重复恢复不产生新写）；
//  未提交事务从未到达页文件（no-steal），天然全有或全无。
type Store struct {
	walF   File
	dbF    File
	lsn    uint64
	walOff int64
	txnSeq uint64
	// OnCommit 若设置，在 commit 记录落盘之后、Commit 返回之前调用
	// （即“提交确认”点，测试用它核对 ack 一定晚于 WAL 刷盘）。
	OnCommit func(txnID uint64)
}

// Open 打开存储并执行恢复。
func Open(walF, dbF File) (*Store, error) {
	s := &Store{walF: walF, dbF: dbF}
	if err := s.recover(); err != nil {
		return nil, err
	}
	return s, nil
}

// Txn 缓冲一笔事务的页写，直到 Commit。
type Txn struct {
	id     uint64
	order  []uint32
	writes map[uint32][]byte
}

func (s *Store) Begin() *Txn {
	s.txnSeq++
	return &Txn{id: s.txnSeq, writes: make(map[uint32][]byte)}
}

// Set 缓冲一次页写，data 必须是 PageDataSize 字节。
func (t *Txn) Set(pageID uint32, data []byte) {
	if len(data) != PageDataSize {
		panic("walstore: page data must be PageDataSize bytes")
	}
	if _, ok := t.writes[pageID]; !ok {
		t.order = append(t.order, pageID)
	}
	t.writes[pageID] = append([]byte(nil), data...)
}

// Commit 使事务持久，然后才确认。
func (s *Store) Commit(t *Txn) error {
	recs := make([][]byte, 0, len(t.order)+1)
	pageLSNs := make(map[uint32]uint64, len(t.order))
	for _, pid := range t.order {
		s.lsn++
		pageLSNs[pid] = s.lsn
		recs = append(recs, encodeDataRecord(s.lsn, t.id, pid, t.writes[pid]))
	}
	s.lsn++
	recs = append(recs, encodeCommitRecord(s.lsn, t.id))
	for _, r := range recs {
		if err := s.walAppend(r); err != nil {
			return err
		}
	}
	// 提交点：恢复所需信息（数据记录 + commit 记录）在此落盘。
	if ann, ok := s.walF.(interface{ AnnotateNextSync(string) }); ok {
		ann.AnnotateNextSync(fmt.Sprintf("commit:%d", t.id))
	}
	if err := s.walF.Sync(); err != nil {
		return err
	}
	// no-steal：只有已提交事务的页才允许进入页文件。
	// 这些写刻意不 Sync，崩溃丢失后由恢复从 WAL 重做。
	for _, pid := range t.order {
		if err := s.writePage(pid, pageLSNs[pid], t.writes[pid]); err != nil {
			return err
		}
	}
	if s.OnCommit != nil {
		s.OnCommit(t.id)
	}
	return nil
}

// Checkpoint 把页文件刷盘。原型不截断 WAL（恢复靠 pageLSN 幂等跳过）。
func (s *Store) Checkpoint() error { return s.dbF.Sync() }

// Get 读取页数据部分（未写过的页返回全零）。
func (s *Store) Get(pageID uint32) ([]byte, error) {
	page := make([]byte, PageSize)
	off := int64(pageID) * PageSize
	size, err := s.dbF.Size()
	if err != nil {
		return nil, err
	}
	if off+PageSize <= size {
		if _, err := readFull(s.dbF, page, off); err != nil {
			return nil, err
		}
	}
	return page[pageLSNSize:], nil
}

func (s *Store) walAppend(rec []byte) error {
	n, err := s.walF.WriteAt(rec, s.walOff)
	if err != nil {
		return fmt.Errorf("walstore: wal append failed: %v", err)
	}
	s.walOff += int64(n)
	return nil
}

func (s *Store) writePage(pageID uint32, pageLSN uint64, data []byte) error {
	page := make([]byte, PageSize)
	binary.BigEndian.PutUint64(page[:pageLSNSize], pageLSN)
	copy(page[pageLSNSize:], data)
	_, err := s.dbF.WriteAt(page, int64(pageID)*PageSize)
	return err
}

func (s *Store) pageLSN(pageID uint32) (uint64, error) {
	off := int64(pageID) * PageSize
	size, err := s.dbF.Size()
	if err != nil {
		return 0, err
	}
	if off+PageSize > size {
		return 0, nil
	}
	var hdr [pageLSNSize]byte
	if _, err := readFull(s.dbF, hdr[:], off); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint64(hdr[:]), nil
}

func (s *Store) recover() error {
	recs, validEnd, err := scanWAL(s.walF)
	if err != nil {
		return err
	}
	committed := make(map[uint64]bool)
	var maxLSN, maxTxn uint64
	for _, r := range recs {
		if r.lsn > maxLSN {
			maxLSN = r.lsn
		}
		if r.txnID > maxTxn {
			maxTxn = r.txnID
		}
		if r.commit {
			committed[r.txnID] = true
		}
	}
	// 按 LSN 顺序重做已提交事务；pageLSN 保证幂等。
	for _, r := range recs {
		if r.commit || !committed[r.txnID] {
			continue
		}
		cur, err := s.pageLSN(r.pageID)
		if err != nil {
			return err
		}
		if cur < r.lsn {
			if err := s.writePage(r.pageID, r.lsn, r.data); err != nil {
				return err
			}
		}
	}
	if err := s.dbF.Sync(); err != nil {
		return err
	}
	s.lsn = maxLSN
	s.txnSeq = maxTxn
	s.walOff = validEnd
	return nil
}
