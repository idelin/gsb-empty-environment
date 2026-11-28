package hlctxn

import (
	"errors"
)

// maxAutoRetries 限制自动重试辅助函数的重启/冲突重试次数。
const maxAutoRetries = 200

// Transaction 是一个并发控制事务。readTS 可因不确定性窗口被推高，
// maxTS 在整个生命周期内保持不变（防止不确定性窗口无限漂移）。
type Transaction struct {
	cluster *Cluster
	record  *TxnRecord

	readTS Timestamp
	maxTS  Timestamp

	// observed[N] 是本事务在节点 N 上观测到的节点安全上界（节点 HLC 读数），
	// 用于消除同一节点的重复不确定性：仅首次接触节点时采样。
	observed map[int]Timestamp

	// restartedOn 记录已触发过硬重启的节点，同一节点至多重启一次。
	restartedOn map[int]bool
}

// Begin 在协调节点上开启一个新事务，读取初始 readTS 并固定 maxTS。
func (c *Cluster) Begin(coordinator int, priority int32) *Transaction {
	c.mu.Lock()
	defer c.mu.Unlock()
	if coordinator < 0 || coordinator >= len(c.nodes) {
		panic("hlctxn: invalid coordinator node")
	}
	id := c.nextTxnID
	c.nextTxnID++

	readTS := c.nodes[coordinator].HLC.Now()
	rec := &TxnRecord{
		ID:          id,
		Coordinator: coordinator,
		Priority:    priority,
		Status:      Pending,
		WriteTS:     readTS,
		trueStartNS: c.trueClock.Now(),
		IntentKeys:  make(map[string]struct{}),
	}
	c.records[id] = rec

	return &Transaction{
		cluster:     c,
		record:      rec,
		readTS:      readTS,
		maxTS:       readTS.Add(c.maxOffset.Nanoseconds()),
		observed:    map[int]Timestamp{coordinator: readTS},
		restartedOn: make(map[int]bool),
	}
}

func (txn *Transaction) ID() uint64        { return txn.record.ID }
func (txn *Transaction) Priority() int32   { return txn.record.Priority }
func (txn *Transaction) Coordinator() int  { return txn.record.Coordinator }
func (txn *Transaction) ReadTS() Timestamp { return txn.readTS }
func (txn *Transaction) MaxTS() Timestamp  { return txn.maxTS }
func (txn *Transaction) Status() TxnStatus { return txn.record.Status }

// TrueStartNS 返回无偏斜基准钟记录的事务真实开始时间。
func (txn *Transaction) TrueStartNS() int64 { return txn.record.trueStartNS }

// Observed 返回某节点的安全上界；未接触过该节点时返回 false。
func (txn *Transaction) Observed(node int) (Timestamp, bool) {
	ts, ok := txn.observed[node]
	return ts, ok
}

func (txn *Transaction) checkLive() error {
	switch txn.record.Status {
	case Aborted:
		return ErrAborted
	case Committed:
		return ErrCommitted
	}
	return nil
}

// contactNode 在首次接触节点时采样其 HLC 作为节点安全上界 observed[N]。
func (txn *Transaction) contactNode(node int) {
	if _, ok := txn.observed[node]; !ok {
		txn.observed[node] = txn.cluster.nodes[node].HLC.Now()
	}
}

// forwardReadTS 把 readTS 推高到 newTS（maxTS 不变），并把本事务已有
// 意图与事务记录 WriteTS 同步前推，保证提交戳 >= 全部意图时间戳。
func (txn *Transaction) forwardReadTS(newTS Timestamp) {
	if !newTS.Greater(txn.readTS) {
		return
	}
	txn.readTS = newTS
	rec := txn.record
	if newTS.Greater(rec.WriteTS) {
		rec.WriteTS = newTS
	}
	for key := range rec.IntentKeys {
		node := txn.cluster.nodeForKey(key)
		ks := txn.cluster.keyStateOn(node, key)
		if ks.intent != nil && ks.intent.TxnID == rec.ID {
			ks.intent.TS = newTS
		}
	}
}

// higherPriority 定义严格全序：先比优先级，相等时以事务 ID 大者为胜，
// 杜绝同优先级冲突时的循环等待（死锁）。
func higherPriority(a, b *TxnRecord) bool {
	if a.Priority != b.Priority {
		return a.Priority > b.Priority
	}
	return a.ID > b.ID
}

// abortTxnLocked 执行 PENDING -> ABORTED 原子单向迁移。
// keepIntents=true（LazyResolution 模式）时保留意图，等待后来者解析。
func (c *Cluster) abortTxnLocked(rec *TxnRecord, keepIntents bool) {
	if rec.Status != Pending {
		return
	}
	rec.Status = Aborted
	c.stats.TxnAborts++
	if keepIntents {
		return
	}
	for key := range rec.IntentKeys {
		node := c.nodeForKey(key)
		ks := c.keyStateOn(node, key)
		if ks.intent != nil && ks.intent.TxnID == rec.ID {
			ks.intent = nil
			c.stats.IntentResolved++
		}
	}
	rec.IntentKeys = make(map[string]struct{})
}

// resolveIntentLocked 原地解析单个意图：COMMITTED 按真实提交戳处理，
// ABORTED 直接丢弃；PENDING 不做任何事。
func (c *Cluster) resolveIntentLocked(ks *keyState, node int, key string) {
	if ks.intent == nil {
		return
	}
	rec, ok := c.records[ks.intent.TxnID]
	if !ok {
		ks.intent = nil
		return
	}
	switch rec.Status {
	case Committed:
		// 提交版本已在 Commit 时写入版本链；这里清理意图并做因果 HLC 合并。
		c.nodes[node].HLC.Update(rec.CommitTS)
		ks.intent = nil
		delete(rec.IntentKeys, key)
		c.stats.IntentResolved++
	case Aborted:
		ks.intent = nil
		delete(rec.IntentKeys, key)
		c.stats.IntentResolved++
	}
}

// handleIntentLocked 处理读/写事务撞上的 PENDING 意图。
//   - 意图时间戳超出 reader.maxTS：属未来事件，不推不杀，直接忽略；
//   - 写者胜出：原子中止低优事务并清理其意图；
//   - 读者胜出：PushTimestamp，把冲突方意图/记录推高到 readTS 之后；
//   - 己方落败：返回 ConflictError，由调用方退避后整体重试。
func (txn *Transaction) handleIntentLocked(ks *keyState, node int, key string, isWrite bool) error {
	c := txn.cluster
	c.resolveIntentLocked(ks, node, key)
	if ks.intent == nil {
		return nil
	}
	other, ok := c.records[ks.intent.TxnID]
	if !ok {
		return ErrUnknownTxn
	}
	if other.Status != Pending {
		c.resolveIntentLocked(ks, node, key)
		return nil
	}
	if !ks.intent.TS.LessEq(txn.maxTS) {
		// 意图时间戳超出 reader.maxTS：无法影响本事务快照，跳过意图走版本链。
		return errIntentBeyondMax
	}
	win := higherPriority(txn.record, other)
	if !win {
		return &ConflictError{Key: key, OtherTxn: other.ID, ReaderTxn: txn.record.ID}
	}
	if isWrite {
		// 高优写事务：低优事务不可能再以旧戳提交，直接中止并接管该键。
		c.abortTxnLocked(other, false)
		c.resolveIntentLocked(ks, node, key) // LazyResolution 下也立即清理胜者要写的键
		return nil
	}
	// 高优读者 PushTimestamp：新戳严格大于当前 readTS 与对方 WriteTS。
	newTS := txn.readTS.Next()
	if !other.WriteTS.Less(newTS) {
		newTS = other.WriteTS.Next()
	}
	other.WriteTS = newTS
	ks.intent.TS = newTS
	c.stats.TimestampPushes++
	return nil
}

// uncertaintyScan 对节点上的已提交版本链执行不确定性窗口检查。版本链按 ts 降序。
// 返回本地前推后应直接采用的版本；nil 表示调用方按当前 readTS 自行定位。
func (txn *Transaction) uncertaintyScan(ks *keyState, node int) (*mvccVersion, error) {
	c := txn.cluster
	obs := txn.observed[node]
	for i := range ks.versions {
		v := &ks.versions[i]
		if v.ts.LessEq(txn.readTS) {
			return nil, nil // 命中当前快照可见版本，交给调用方统一返回
		}
		// observed[N] 是本事务在节点 N 上直接读到的 HLC 上界。
		// HLC 单调性保证：v.ts <= observed[N] 的版本必然先于观测事件提交，
		// 即使 v.ts 超出全局 maxTS 也属节点本地确定过去（外部一致性关键规则）。
		if v.ts.LessEq(obs) {
			// 静默前推 readTS 并直接采用该版本（无需重启，也不会在同节点循环）。
			txn.forwardReadTS(v.ts)
			c.stats.LocalForwards++
			return v, nil
		}
		// v.ts > observed[N] 且 v.ts ∈ (readTS, maxTS]：无法判定物理先后，硬重启。
		if v.ts.LessEq(txn.maxTS) {
			if txn.restartedOn[node] {
				return nil, ErrTooManyRestarts // 防御性断言：同节点不应二次重启
			}
			txn.restartedOn[node] = true
			txn.observed[node] = v.ts
			txn.readTS = v.ts // maxTS 刻意保持不变
			c.stats.ReadRestarts++
			return nil, &ReadRestartError{Node: node, NewReadTS: v.ts, MaxTS: txn.maxTS}
		}
		// v.ts > maxTS 且 v.ts > observed[N]：确定性未来，跳过并继续找更旧版本。
		continue
	}
	return nil, nil
}

// Get 读取键在事务快照下的值。ok=false 表示键不存在。
func (txn *Transaction) Get(key string) (value string, ok bool, err error) {
	c := txn.cluster
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := txn.checkLive(); err != nil {
		return "", false, err
	}
	node := c.nodeForKey(key)
	txn.contactNode(node)
	ks := c.keyStateOn(node, key)

	// 读己之写：命中本事务未决意图。
	if ks.intent != nil && ks.intent.TxnID == txn.record.ID {
		return ks.intent.Value, true, nil
	}
	if ks.intent != nil {
		if err := txn.handleIntentLocked(ks, node, key, false); err != nil && !errors.Is(err, errIntentBeyondMax) {
			return "", false, err
		}
	}
	visible, err := txn.uncertaintyScan(ks, node)
	if err != nil {
		return "", false, err
	}
	if visible != nil {
		return visible.value, true, nil
	}
	for _, v := range ks.versions {
		if v.ts.LessEq(txn.readTS) {
			return v.value, true, nil
		}
	}
	return "", false, nil
}

// Put 在键上写入（或覆盖）指向本事务记录的写意图。
func (txn *Transaction) Put(key, value string) error {
	c := txn.cluster
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := txn.checkLive(); err != nil {
		return err
	}
	node := c.nodeForKey(key)
	txn.contactNode(node)
	ks := c.keyStateOn(node, key)

	// 同事务重复写同一键：原地覆盖。
	if ks.intent != nil && ks.intent.TxnID == txn.record.ID {
		ks.intent.Value = value
		ks.intent.TS = txn.readTS
		return nil
	}
	if ks.intent != nil {
		if err := txn.handleIntentLocked(ks, node, key, true); err != nil && !errors.Is(err, errIntentBeyondMax) {
			return err
		}
	}
	// 写同样要经过不确定性窗口检查，避免覆盖物理上可能更早的已提交版本。
	if _, err := txn.uncertaintyScan(ks, node); err != nil {
		return err
	}
	ks.intent = &Intent{
		TxnID: txn.record.ID,
		Node:  node,
		TS:    txn.readTS,
		Value: value,
	}
	txn.record.IntentKeys[key] = struct{}{}
	if txn.readTS.Greater(txn.record.WriteTS) {
		txn.record.WriteTS = txn.readTS
	}
	return nil
}

// Commit 提交事务：PENDING -> COMMITTED 原子迁移，全部意图以统一
// commitTS 原子对外可见，commitTS >= 所有意图的最大被推高戳。
func (txn *Transaction) Commit() (Timestamp, error) {
	c := txn.cluster
	c.mu.Lock()
	defer c.mu.Unlock()
	rec := txn.record
	switch rec.Status {
	case Aborted:
		return Timestamp{}, ErrAborted
	case Committed:
		return rec.CommitTS, ErrCommitted
	}
	commitTS := c.nodes[rec.Coordinator].HLC.Now()
	if rec.WriteTS.Greater(commitTS) {
		commitTS = rec.WriteTS // 被推高的意图戳是提交戳下界
	}

	// 状态机单向迁移与版本对外可见在集群锁内原子完成。
	rec.Status = Committed
	rec.CommitTS = commitTS
	rec.trueCommitNS = c.trueClock.Now()
	c.stats.TxnCommits++

	for key := range rec.IntentKeys {
		node := c.nodeForKey(key)
		ks := c.keyStateOn(node, key)
		if ks.intent != nil && ks.intent.TxnID == rec.ID {
			ks.insertVersion(mvccVersion{
				ts:    commitTS,
				value: ks.intent.Value,
				txnID: rec.ID,
			})
			// 因果合并：存储节点 HLC 必须前进到 commitTS，这是
			// observed[N] 外部一致性论证的关键不变量。
			c.nodes[node].HLC.Update(commitTS)
			if !c.LazyResolution {
				ks.intent = nil
				c.stats.IntentResolved++
			}
		}
	}
	if !c.LazyResolution {
		rec.IntentKeys = make(map[string]struct{})
	}
	return commitTS, nil
}

// Abort 中止事务：PENDING -> ABORTED 原子迁移并清理全部意图。
func (txn *Transaction) Abort() error {
	c := txn.cluster
	c.mu.Lock()
	defer c.mu.Unlock()
	if txn.record.Status != Pending {
		return nil
	}
	c.abortTxnLocked(txn.record, c.LazyResolution)
	return nil
}
