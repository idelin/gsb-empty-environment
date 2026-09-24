// Package pagedb 实现一个基于预写日志（WAL）的页更新事务存储。
//
// 核心规则：
//   - 日志先于数据页持久化：COMMIT 记录 fsync 成功即事务持久（提交点），
//     数据页在此之后才刷盘。
//   - 恢复时只重放带有完整 COMMIT 标记的事务。
//   - 页带 CRC32 校验和与版本号（= 写入它的日志 LSN），
//     仅当 记录LSN > 页版本 时才重放，因此重放任意多次结果相同。
//   - WAL 末尾的残缺记录识别为撕裂尾并截断；中间损坏则报错。
package pagedb

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
)

const (
	walFileName   = "wal.log"
	pagesFileName = "pages.dat"
)

// ErrTxDone 表示事务已提交或中止。
var ErrTxDone = errors.New("pagedb: transaction already finished")

// ErrDBClosed 表示数据库已关闭。
var ErrDBClosed = errors.New("pagedb: database is closed")

// Failpoints 是故障注入点，仅用于测试与演示，模拟进程崩溃/断电。
// 钩子返回非 nil 错误时，后续步骤不再执行，效果等同于进程当场被杀。
type Failpoints struct {
	// AfterWALSyncBeforePageFlush 在 Commit 的提交点（WAL 已 fsync）
	// 之后、数据页刷盘之前触发。返回错误可模拟"已提交但页未刷盘"的崩溃。
	AfterWALSyncBeforePageFlush func() error
}

// DB 是存储句柄。所有文件都位于 Open 指定的目录中。
type DB struct {
	dir      string
	wal      *os.File
	pages    *os.File
	mu       sync.Mutex
	nextLSN  uint64
	nextTxID uint64
	closed   bool

	// Fail 故障注入点。生产路径应为零值。
	Fail Failpoints
}

// Open 打开（必要时创建）dir 下的存储，并执行崩溃恢复。
func Open(dir string) (*DB, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	walPath := filepath.Join(dir, walFileName)
	pagesPath := filepath.Join(dir, pagesFileName)

	scan, err := scanWAL(walPath)
	if err != nil {
		return nil, err
	}

	wal, err := os.OpenFile(walPath, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	pages, err := os.OpenFile(pagesPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		wal.Close()
		return nil, err
	}
	db := &DB{dir: dir, wal: wal, pages: pages, nextLSN: 1, nextTxID: 1}

	if scan.tornTail {
		// 撕裂尾：截断到最后的合法记录，后续追加从干净边界继续。
		if err := wal.Truncate(scan.goodLen); err != nil {
			db.Close()
			return nil, err
		}
	}
	if err := db.recover(scan.records); err != nil {
		db.Close()
		return nil, err
	}
	// 保证文件创建/截断等目录项变更持久。
	if err := fsyncDir(dir); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// recover 重放所有带 COMMIT 标记的事务；未提交事务的 UPDATE 记录被丢弃。
func (db *DB) recover(recs []record) error {
	committed := make(map[uint64]bool)
	var updates []record
	var maxLSN, maxTxID uint64
	for _, r := range recs {
		if r.lsn > maxLSN {
			maxLSN = r.lsn
		}
		if r.txid > maxTxID {
			maxTxID = r.txid
		}
		switch r.typ {
		case recCommit:
			committed[r.txid] = true
		case recUpdate:
			updates = append(updates, r)
		}
	}
	// updates 按文件顺序即 LSN 升序；同页多次更新时高 LSN 后应用。
	for _, r := range updates {
		if !committed[r.txid] {
			continue
		}
		ver, err := db.pageVersion(r.pageID)
		if err != nil {
			return err
		}
		if r.lsn > ver {
			if err := db.writePage(r.pageID, r.lsn, r.data); err != nil {
				return err
			}
		}
	}
	if err := db.pages.Sync(); err != nil {
		return err
	}
	db.nextLSN = maxLSN + 1
	db.nextTxID = maxTxID + 1
	return nil
}

// pageVersion 返回页当前版本。页不存在为 0；页损坏按 0 处理，
// 让 WAL 重放重建该页（WAL 是先于页持久化的权威副本）。
func (db *DB) pageVersion(pageID uint64) (uint64, error) {
	buf := make([]byte, PageSize)
	n, err := db.pages.ReadAt(buf, int64(pageID)*PageSize)
	if err != nil && !errors.Is(err, io.EOF) {
		return 0, err
	}
	if n < PageSize {
		return 0, nil
	}
	ver, _, err := decodePage(buf)
	if errors.Is(err, ErrPageCorrupt) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return ver, nil
}

// writePage 整页写入。调用方需持有 db.mu 或处于单线程的恢复阶段。
func (db *DB) writePage(pageID, version uint64, data []byte) error {
	_, err := db.pages.WriteAt(encodePage(version, data), int64(pageID)*PageSize)
	return err
}

// Get 读取页的当前已提交镜像，返回数据与版本。
// 页从未被提交写入时返回 (nil, 0, nil)。
func (db *DB) Get(pageID uint64) ([]byte, uint64, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.closed {
		return nil, 0, ErrDBClosed
	}
	buf := make([]byte, PageSize)
	n, err := db.pages.ReadAt(buf, int64(pageID)*PageSize)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, 0, err
	}
	if n < PageSize {
		return nil, 0, nil
	}
	ver, data, err := decodePage(buf)
	if err != nil {
		return nil, 0, err
	}
	return data, ver, nil
}

// Close 关闭存储。已提交事务在 Commit 返回前已持久化，Close 无需额外刷盘。
func (db *DB) Close() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.closed {
		return nil
	}
	db.closed = true
	err1 := db.wal.Close()
	err2 := db.pages.Close()
	return errors.Join(err1, err2)
}

// Tx 是一个页更新事务。
type Tx struct {
	db     *DB
	id     uint64
	staged map[uint64]stagedPage
	done   bool
}

type stagedPage struct {
	lsn  uint64
	data []byte
}

// Begin 开启一个事务。
func (db *DB) Begin() (*Tx, error) {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.closed {
		return nil, ErrDBClosed
	}
	id := db.nextTxID
	db.nextTxID++
	return &Tx{db: db, id: id, staged: make(map[uint64]stagedPage)}, nil
}

// Set 暂存一次整页写入，并把 UPDATE 记录追加到 WAL（不 fsync）。
// 未提交的记录对读者不可见，恢复时也会被忽略。
func (tx *Tx) Set(pageID uint64, data []byte) error {
	if len(data) > MaxPageData {
		return ErrPageDataTooLarge
	}
	tx.db.mu.Lock()
	defer tx.db.mu.Unlock()
	if tx.done {
		return ErrTxDone
	}
	if tx.db.closed {
		return ErrDBClosed
	}
	lsn := tx.db.nextLSN
	tx.db.nextLSN++
	rec := record{typ: recUpdate, lsn: lsn, txid: tx.id, pageID: pageID, data: data}
	if _, err := tx.db.wal.Write(encodeRecord(rec)); err != nil {
		return err
	}
	tx.staged[pageID] = stagedPage{lsn: lsn, data: append([]byte(nil), data...)}
	return nil
}

// Commit 提交事务：先写 COMMIT 记录并 fsync WAL（提交点），
// 再把暂存页写入页文件并 fsync。提交点之前崩溃，事务整体不可见；
// 提交点之后崩溃，恢复时整体重放。
func (tx *Tx) Commit() error {
	tx.db.mu.Lock()
	defer tx.db.mu.Unlock()
	if tx.done {
		return ErrTxDone
	}
	tx.done = true
	db := tx.db
	if db.closed {
		return ErrDBClosed
	}

	lsn := db.nextLSN
	db.nextLSN++
	if _, err := db.wal.Write(encodeRecord(record{typ: recCommit, lsn: lsn, txid: tx.id})); err != nil {
		return err
	}
	// 提交点：WAL 持久化后事务才算成立。
	if err := db.wal.Sync(); err != nil {
		return err
	}
	if db.Fail.AfterWALSyncBeforePageFlush != nil {
		if err := db.Fail.AfterWALSyncBeforePageFlush(); err != nil {
			return err // 模拟崩溃：页不刷盘，由下次 Open 的恢复重放
		}
	}
	for pageID, sp := range tx.staged {
		if err := db.writePage(pageID, sp.lsn, sp.data); err != nil {
			return err
		}
	}
	return db.pages.Sync()
}

// Abort 中止事务。已追加到 WAL 的 UPDATE 记录没有 COMMIT 标记，
// 恢复时会被忽略，因此不会暴露半个事务。
func (tx *Tx) Abort() {
	tx.db.mu.Lock()
	defer tx.db.mu.Unlock()
	tx.done = true
}

func fsyncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
