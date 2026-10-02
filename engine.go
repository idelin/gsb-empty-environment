// Package lsm 实现 LSM 树内存表向 SSTable 持久化的分层转换器:
//
//	MemTable --(达到阈值, 冻结)--> Immutable MemTable --(后台单线程顺序刷盘)--> Level 0 SSTable
//
// 每个活跃 MemTable 绑定一个 WAL 段; 轮转时旧段随 Immutable 一起进入刷盘队列,
// 刷盘落盘(fsync)完成后旧 WAL 段才被删除, 崩溃时可从残留 WAL 段完整恢复。
package lsm

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// DefaultMemtableThreshold 是 MemTable 冻结阈值, 4MB。
const DefaultMemtableThreshold = 4 << 20

// ErrClosed 在引擎关闭后的写入上返回。
var ErrClosed = errors.New("lsm: engine closed")

// Options 引擎配置。
type Options struct {
	// MemtableThreshold 是 MemTable 近似大小阈值(字节), 默认 4MB。
	MemtableThreshold int64
	// SyncWrites 为 true 时每条 WAL 记录都 fsync(更耐用但更慢)。
	SyncWrites bool
	// FlushHook 若非空, 在每次刷盘开始前调用(测试钩子)。
	FlushHook func()
}

// Stats 是引擎运行状态快照。
type Stats struct {
	Rotations   int // 累计轮转次数(冻结次数)
	Flushes     int // 累计完成的刷盘次数
	QueueLength int // 待刷盘的 Immutable 队列长度
	L0Files     int // 当前 Level 0 SSTable 文件数
}

// flushJob 是一个待刷盘的 Immutable MemTable 及其绑定的 WAL 段。
type flushJob struct {
	mt      *memtable
	walPath string
}

// Engine 是 LSM 内存表分层转换引擎。
type Engine struct {
	dir  string
	opts Options

	mu        sync.Mutex
	active    *memtable
	activeWAL *wal
	walSeq    int
	sstSeq    int
	queue     []flushJob  // 待刷盘队列, FIFO
	frozen    []*memtable // 已冻结未落盘的表(含队列中与刷盘中的), 旧->新
	l0        []*sstable  // Level 0 文件, 旧->新
	rotations int
	flushes   int
	flushErr  error
	closed    bool

	signal chan struct{} // 唤醒刷盘协程
	done   chan struct{} // 关闭信号
	wg     sync.WaitGroup
}

func walFileName(dir string, seq int) string {
	return filepath.Join(dir, fmt.Sprintf("wal-%06d.log", seq))
}

func sstFileName(dir string, seq int) string {
	return filepath.Join(dir, fmt.Sprintf("L0-%06d.sst", seq))
}

// Open 打开(必要时创建)引擎, 并从残留的 WAL 段与 SSTable 文件恢复状态。
func Open(dir string, opts *Options) (*Engine, error) {
	o := Options{MemtableThreshold: DefaultMemtableThreshold}
	if opts != nil {
		o = *opts
		if o.MemtableThreshold <= 0 {
			o.MemtableThreshold = DefaultMemtableThreshold
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	e := &Engine{
		dir:    dir,
		opts:   o,
		signal: make(chan struct{}, 1),
		done:   make(chan struct{}),
	}
	if err := e.recover(); err != nil {
		return nil, err
	}
	e.wg.Add(1)
	go e.flushLoop()
	if len(e.queue) > 0 {
		e.kickFlusher()
	}
	return e, nil
}

// recover 扫描数据目录: 载入全部 L0 SSTable, 并按序号回放全部 WAL 段。
// 最后一个段成为活跃 MemTable, 更早的段恢复为 Immutable 并重新排队刷盘。
func (e *Engine) recover() error {
	ents, err := os.ReadDir(e.dir)
	if err != nil {
		return err
	}
	type seg struct {
		seq  int
		path string
	}
	var wals, ssts []seg
	for _, ent := range ents {
		name := ent.Name()
		if strings.HasSuffix(name, ".tmp") {
			os.Remove(filepath.Join(e.dir, name)) // 清理崩溃遗留的半成品
			continue
		}
		if strings.HasPrefix(name, "wal-") && strings.HasSuffix(name, ".log") {
			if n, err := strconv.Atoi(name[4 : len(name)-4]); err == nil {
				wals = append(wals, seg{n, filepath.Join(e.dir, name)})
			}
		}
		if strings.HasPrefix(name, "L0-") && strings.HasSuffix(name, ".sst") {
			if n, err := strconv.Atoi(name[3 : len(name)-4]); err == nil {
				ssts = append(ssts, seg{n, filepath.Join(e.dir, name)})
			}
		}
	}
	sort.Slice(wals, func(i, j int) bool { return wals[i].seq < wals[j].seq })
	sort.Slice(ssts, func(i, j int) bool { return ssts[i].seq < ssts[j].seq })

	for _, s := range ssts {
		f, err := openSSTable(s.path)
		if err != nil {
			return err
		}
		e.l0 = append(e.l0, f)
		if s.seq >= e.sstSeq {
			e.sstSeq = s.seq + 1
		}
	}

	if len(wals) == 0 {
		e.active = newMemtable()
		e.activeWAL, err = createWAL(walFileName(e.dir, e.walSeq))
		return err
	}
	// 除最后一段外, 其余恢复为 Immutable 并排队刷盘。
	for _, wseg := range wals[:len(wals)-1] {
		mt := newMemtable()
		if err := replayWAL(wseg.path, func(t byte, k, v []byte) {
			if t == recordTypeDel {
				mt.delete(k)
			} else {
				mt.put(k, v)
			}
		}); err != nil {
			return err
		}
		mt.freeze()
		e.queue = append(e.queue, flushJob{mt: mt, walPath: wseg.path})
		e.frozen = append(e.frozen, mt)
	}
	last := wals[len(wals)-1]
	e.active = newMemtable()
	if err := replayWAL(last.path, func(t byte, k, v []byte) {
		if t == recordTypeDel {
			e.active.delete(k)
		} else {
			e.active.put(k, v)
		}
	}); err != nil {
		return err
	}
	e.walSeq = last.seq
	e.activeWAL, err = openWALAppend(last.path)
	return err
}

// Put 写入一条键值记录: 先追加 WAL, 再写 MemTable。
// 达到阈值时在 O(1) 内完成轮转(冻结旧表、派生新表与新 WAL 段),
// 刷盘由后台单线程完成, 前台写入不因刷盘而停顿。
func (e *Engine) Put(key, value []byte) error {
	return e.apply(recordTypePut, key, value)
}

// Delete 写入墓碑记录。
func (e *Engine) Delete(key []byte) error {
	return e.apply(recordTypeDel, key, nil)
}

func (e *Engine) apply(recType byte, key, value []byte) error {
	for {
		e.mu.Lock()
		if e.closed {
			e.mu.Unlock()
			return ErrClosed
		}
		if e.active.approxSize() >= e.opts.MemtableThreshold {
			if err := e.rotateLocked(); err != nil {
				e.mu.Unlock()
				return err
			}
		}
		mt, w := e.active, e.activeWAL
		e.mu.Unlock()

		if err := w.append(recType, key, value); err != nil {
			return err
		}
		if e.opts.SyncWrites {
			if err := w.sync(); err != nil {
				return err
			}
		}
		var ok bool
		if recType == recordTypeDel {
			ok = mt.delete(key)
		} else {
			ok = mt.put(key, value)
		}
		if ok {
			return nil
		}
		// 极少见: 抓取活跃表后恰发生轮转, 记录已落入旧 WAL 段。
		// 旧段刷盘前崩溃回放与删除均安全(同 key 同值幂等),
		// 此处对新活跃表重试, 保证记录进入正确的 (WAL, MemTable) 对。
	}
}

// rotateLocked 冻结当前 MemTable 并派生新表与新 WAL 段。调用方须持有 e.mu。
// 顺序: fsync 旧段 -> 创建并 fsync 新段 -> 入队旧表 -> 切换活跃表。
func (e *Engine) rotateLocked() error {
	if err := e.activeWAL.sync(); err != nil {
		return err
	}
	e.walSeq++
	nw, err := createWAL(walFileName(e.dir, e.walSeq))
	if err != nil {
		return err
	}
	if err := nw.sync(); err != nil {
		nw.close()
		return err
	}
	e.active.freeze()
	e.queue = append(e.queue, flushJob{mt: e.active, walPath: e.activeWAL.path})
	e.frozen = append(e.frozen, e.active)
	e.active = newMemtable()
	e.activeWAL = nw
	e.rotations++
	e.kickFlusher()
	return nil
}

func (e *Engine) kickFlusher() {
	select {
	case e.signal <- struct{}{}:
	default:
	}
}

// flushLoop 是后台唯一的刷盘协程: 单线程、按 FIFO 顺序把 Immutable
// MemTable 刷成 Level 0 SSTable, 落盘后才删除对应 WAL 段。
func (e *Engine) flushLoop() {
	defer e.wg.Done()
	for {
		select {
		case <-e.done:
			e.drain()
			return
		case <-e.signal:
			e.drain()
		}
	}
}

func (e *Engine) drain() {
	for {
		e.mu.Lock()
		if len(e.queue) == 0 {
			e.mu.Unlock()
			return
		}
		job := e.queue[0]
		e.queue = e.queue[1:]
		e.mu.Unlock()
		e.flush(job)
	}
}

func (e *Engine) flush(job flushJob) {
	if e.opts.FlushHook != nil {
		e.opts.FlushHook()
	}
	path := sstFileName(e.dir, e.sstSeq)
	if err := writeSSTable(path, job.mt.sorted()); err != nil {
		e.mu.Lock()
		if e.flushErr == nil {
			e.flushErr = err
		}
		e.mu.Unlock()
		return // 保留 WAL 段, 下次 Open 可恢复重试
	}
	e.sstSeq++
	sst, err := openSSTable(path)
	if err != nil {
		e.mu.Lock()
		if e.flushErr == nil {
			e.flushErr = err
		}
		e.mu.Unlock()
		return
	}
	e.mu.Lock()
	e.l0 = append(e.l0, sst)
	for i, mt := range e.frozen {
		if mt == job.mt {
			e.frozen = append(e.frozen[:i], e.frozen[i+1:]...)
			break
		}
	}
	e.flushes++
	e.mu.Unlock()
	// SSTable 已 fsync 落盘, 旧 WAL 段可安全删除, 完成 WAL 轮转。
	os.Remove(job.walPath)
}

// Get 依次在 活跃表 -> Immutable(新->旧) -> L0 SSTable(新->旧) 中查找。
func (e *Engine) Get(key string) ([]byte, bool, error) {
	e.mu.Lock()
	active := e.active
	frozen := make([]*memtable, len(e.frozen))
	copy(frozen, e.frozen)
	l0 := make([]*sstable, len(e.l0))
	copy(l0, e.l0)
	e.mu.Unlock()

	if ent, ok := active.get(key); ok {
		if ent.deleted {
			return nil, false, nil
		}
		return append([]byte(nil), ent.value...), true, nil
	}
	for i := len(frozen) - 1; i >= 0; i-- {
		if ent, ok := frozen[i].get(key); ok {
			if ent.deleted {
				return nil, false, nil
			}
			return append([]byte(nil), ent.value...), true, nil
		}
	}
	for i := len(l0) - 1; i >= 0; i-- {
		ent, ok, err := l0[i].get(key)
		if err != nil {
			return nil, false, err
		}
		if ok {
			if ent.deleted {
				return nil, false, nil
			}
			return ent.value, true, nil
		}
	}
	return nil, false, nil
}

// Stats 返回当前运行状态快照。
func (e *Engine) Stats() Stats {
	e.mu.Lock()
	defer e.mu.Unlock()
	return Stats{
		Rotations:   e.rotations,
		Flushes:     e.flushes,
		QueueLength: len(e.queue),
		L0Files:     len(e.l0),
	}
}

// Err 返回首个刷盘错误(若有)。
func (e *Engine) Err() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.flushErr
}

// Close 冻结活跃表并等待全部排队任务刷盘完成, 然后关闭所有文件。
func (e *Engine) Close() error {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return e.flushErr
	}
	e.closed = true
	if e.active.approxSize() > 0 {
		if err := e.rotateLocked(); err != nil && e.flushErr == nil {
			e.flushErr = err
		}
	}
	w := e.activeWAL
	e.mu.Unlock()

	close(e.done)
	e.wg.Wait()

	if err := w.close(); err != nil && e.flushErr == nil {
		e.flushErr = err
	}
	// 关闭前的轮转使活跃 MemTable 进入刷盘队列, 最后的 WAL 段不含任何
	// 记录(关闭后拒绝写入), 可安全删除, 保证目录中不留多余 WAL 段。
	if w.length() == 0 {
		os.Remove(w.path)
	}
	e.mu.Lock()
	for _, s := range e.l0 {
		s.close()
	}
	err := e.flushErr
	e.mu.Unlock()
	return err
}
