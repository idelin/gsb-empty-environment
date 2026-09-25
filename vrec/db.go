// Package vrec 实现单写多读（single-writer / multi-reader）的版本化记录存储。
//
// 核心不变量：
//   - 全局单调版本号；每次提交产生一个新版本，每个键在该版本上得到一个新节点。
//   - 读事务（Reader）在开始时固定快照版本，之后只看到 <= 该版本的已提交数据；
//     新写入对老读者不可见，删除对老读者呈现为被删除之前的旧值。
//   - 删除写入墓碑（tombstone）节点，从不就地修改历史。
//   - GC 以“最老活跃读版本”为水位回收：节点链只保留水位之上的节点加一个
//     边界节点；旧读者不关闭，链前缀就不能释放。
//   - 长读不持有任何跨操作锁，因此不阻塞写入；它只通过已注册的快照版本
//     限制 GC 的回收下界。
package vrec

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
)

const (
	walName  = "wal.log"
	tmpName  = "wal.log.compact.tmp"
	dirPerm  = 0755
	filePerm = 0644
)

// 注入点阶段名（见 FailContext.Stage）。
const (
	StageTxnWrite  = "txn.write"   // 事务帧落盘之前（尚未持久化，可干净中止）
	StageTxnSync   = "txn.sync"    // 事务帧已 write+fsync，对其它进程可见之前
	StageGCWrite   = "gc.write"    // 压缩临时文件写入之前（原 WAL 完好，可中止）
	StageGCRename  = "gc.rename"   // 临时文件已 fsync，原子改名之前（可中止）
	StageGCDirSync = "gc.dirfsync" // 已改名、目录 fsync 之前（崩溃后新旧日志都可能生效）
)

type node struct {
	ver  int64
	val  []byte
	tomb bool
}

type readerEntry struct {
	ver    int64
	writer bool
}

// Options 控制打开行为。
type Options struct {
	// FailHook 是故障注入点；为 nil 表示不注入。
	FailHook FailHook
}

// DB 是版本化记录存储。零值不可用，使用 Open 构造。
type DB struct {
	dir string

	// wmu 保护所有内存状态（head、chains、readers、faulted/closed 等）。
	// 长读只在单次 Get 期间持有 RLock，从不跨调用持有。
	wmu sync.RWMutex

	head    int64              // 当前最新已提交版本
	chains  map[string][]*node // key -> 升序版本节点链
	readers map[uint64]readerEntry
	nextID  uint64

	writerToken chan struct{}

	walFile *os.File
	walLen  int64

	failHook FailHook

	faulted atomic.Bool
	closed  bool
}

// FailContext 描述一次注入点调用。
type FailContext struct {
	Stage   string
	Version int64
	Keys    []string
}

// FailHook 返回非 nil 错误即表示该注入点发生故障；返回 ErrSimCrash 表示
// 模拟“进程立即消失”。
type FailHook func(FailContext) error

// Open 打开（必要时创建）dir 下的存储。所有文件都位于调用方指定的目录内。
func Open(dir string, opts *Options) (*DB, error) {
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return nil, err
	}
	walPath := filepath.Join(dir, walName)
	tmpPath := filepath.Join(dir, tmpName)

	// 上次 GC 在改名前后断电可能遗留临时文件：它从未承担过正式日志，直接清除。
	if err := os.Remove(tmpPath); err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	f, err := os.OpenFile(walPath, os.O_RDWR|os.O_CREATE, filePerm)
	if err != nil {
		return nil, err
	}

	res, err := replayWAL(walPath)
	if err != nil {
		f.Close()
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	// 截断撕裂/损坏的尾帧（断电只能破坏日志尾部）。
	if res.validLen < st.Size() {
		if err := f.Truncate(res.validLen); err != nil {
			f.Close()
			return nil, err
		}
		if err := f.Sync(); err != nil {
			f.Close()
			return nil, err
		}
	}
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		f.Close()
		return nil, err
	}

	var hook FailHook
	if opts != nil {
		hook = opts.FailHook
	}
	return &DB{
		dir:         dir,
		head:        res.nextV - 1,
		chains:      res.nodes,
		readers:     make(map[uint64]readerEntry),
		walFile:     f,
		walLen:      res.validLen,
		writerToken: make(chan struct{}, 1),
		failHook:    hook,
	}, nil
}

// HeadVersion 返回最新已提交版本号（初始为 0）。
func (db *DB) HeadVersion() int64 {
	db.wmu.RLock()
	defer db.wmu.RUnlock()
	return db.head
}

func (db *DB) healthy() error {
	if db.closed {
		return ErrClosed
	}
	if db.faulted.Load() {
		return ErrFaulted
	}
	return nil
}

// fail 触发注入点。preDurable 为 true 表示此刻尚未产生任何不可撤销的持久化
// 效果，调用方可干净中止；否则存储进入 faulted，必须重开。
func (db *DB) fail(stage string, ver int64, keys []string, preDurable bool) error {
	if db.failHook == nil {
		return nil
	}
	err := db.failHook(FailContext{Stage: stage, Version: ver, Keys: keys})
	if err == nil {
		return nil
	}
	if !preDurable || errors.Is(err, ErrSimCrash) {
		db.faulted.Store(true)
	}
	return err
}

func (db *DB) register(ver int64, writer bool) uint64 {
	db.nextID++
	id := db.nextID
	db.readers[id] = readerEntry{ver: ver, writer: writer}
	return id
}

func (db *DB) unregister(id uint64) {
	delete(db.readers, id)
}

// searchChain 返回链上版本 <= snap 的最新节点；不存在返回 nil。
// 链按版本升序存储。
func searchChain(chain []*node, snap int64) *node {
	idx := sort.Search(len(chain), func(i int) bool {
		return chain[i].ver > snap
	})
	if idx == 0 {
		return nil
	}
	return chain[idx-1]
}
