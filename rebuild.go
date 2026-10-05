package main

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// VersionedValue 是带版本号的值，用于快照与全量扫描参考数据。
type VersionedValue struct {
	Value   string
	Version uint64
}

// SourceDB 模拟源库：既产生变更事件，又能提供全量扫描（参考真值）。
type SourceDB struct {
	mu   sync.Mutex
	data map[string]VersionedValue
	log  *EventLog
}

func NewSourceDB(log *EventLog) *SourceDB {
	return &SourceDB{data: make(map[string]VersionedValue), log: log}
}

func (db *SourceDB) Put(key, value string) Event {
	db.mu.Lock()
	defer db.mu.Unlock()
	e := db.log.Append(key, OpPut, value)
	db.data[key] = VersionedValue{Value: value, Version: e.Version}
	return e
}

func (db *SourceDB) Delete(key string) Event {
	db.mu.Lock()
	defer db.mu.Unlock()
	e := db.log.Append(key, OpDelete, "")
	delete(db.data, key)
	return e
}

// Scan 全量扫描源库，作为校验新索引的参考真值。
func (db *SourceDB) Scan() map[string]VersionedValue {
	db.mu.Lock()
	defer db.mu.Unlock()
	out := make(map[string]VersionedValue, len(db.data))
	for k, v := range db.data {
		out[k] = v
	}
	return out
}

// Rebuilder 负责从快照构建新索引并追赶增量，直到满足切换条件。
type Rebuilder struct {
	log             *EventLog
	New             *Index
	snapshotVersion uint64
	freezeVersion   uint64
	frozen          bool
}

// NewRebuilder 在 snapshotVersion 处取一致性快照并开始构建新索引。
// 快照之后（版本 > snapshotVersion）的变更需要通过 CatchUp 追赶。
func NewRebuilder(name string, log *EventLog, snapshot map[string]VersionedValue, snapshotVersion uint64) *Rebuilder {
	ix := NewIndex(name, snapshotVersion)
	ix.LoadSnapshot(snapshot)
	return &Rebuilder{log: log, New: ix, snapshotVersion: snapshotVersion}
}

// Freeze 冻结切换目标版本：新索引必须追平到该版本，
// 且冻结点之前的所有未确认变更都必须处理完毕。
func (r *Rebuilder) Freeze() {
	r.freezeVersion = r.log.MaxVersion()
	r.frozen = true
}

// CatchUp 从事件日志重放快照之后的所有事件。
// deliver 模拟投递通道（可乱序、可重发、可丢失，由场景决定）。
func (r *Rebuilder) CatchUp(deliver func([]Event) []Event) {
	events := r.log.Since(r.snapshotVersion)
	if deliver != nil {
		events = deliver(events)
	}
	for _, e := range events {
		r.New.Apply(e)
	}
}

// ReadyToSwitch 报告切换条件是否满足：
// 新索引水位已追到冻结版本，且冻结前所有未确认变更均已处理（无缺口）。
func (r *Rebuilder) ReadyToSwitch() (bool, []uint64) {
	if !r.frozen {
		return false, nil
	}
	missing := r.New.MissingUpto(r.freezeVersion)
	if r.New.Watermark() < r.freezeVersion || len(missing) > 0 {
		return false, missing
	}
	return true, nil
}

// SwitchResult 是一次切换尝试的证据。
type SwitchResult struct {
	Switched      bool
	Reason        string
	FreezeVersion uint64
	OldWatermark  uint64
	NewWatermark  uint64
	Missing       []uint64
	Discrepancies []string
}

func (r SwitchResult) String() string {
	var b strings.Builder
	status := "拒绝切换"
	if r.Switched {
		status = "切换成功"
	}
	fmt.Fprintf(&b, "    结果: %s | 冻结版本=%d 旧索引水位=%d 新索引水位=%d",
		status, r.FreezeVersion, r.OldWatermark, r.NewWatermark)
	if len(r.Missing) > 0 {
		fmt.Fprintf(&b, " | 未确认版本=%v", r.Missing)
	}
	if len(r.Discrepancies) > 0 {
		fmt.Fprintf(&b, " | 校验差异=%d 处", len(r.Discrepancies))
	}
	fmt.Fprintf(&b, "\n    原因: %s", r.Reason)
	return b.String()
}

// Coordinator 持有旧索引并对外提供读服务；切换成功后由新索引接管。
type Coordinator struct {
	mu      sync.RWMutex
	primary *Index
}

func NewCoordinator(primary *Index) *Coordinator {
	return &Coordinator{primary: primary}
}

// Get 读请求始终打到当前主索引——重建期间旧索引持续服务。
func (c *Coordinator) Get(key string) (string, uint64, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.primary.Get(key)
}

// PrimaryName 返回当前主索引名。
func (c *Coordinator) PrimaryName() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.primary.Name()
}

// TrySwitch 校验并尝试切换。只有同时满足：
//  1. 新索引水位追到冻结版本且所有未确认变更已处理；
//  2. 新索引与全量扫描参考数据完全一致；
//
// 才把新索引提升为主索引，否则旧索引继续服务。
func (c *Coordinator) TrySwitch(r *Rebuilder, reference map[string]VersionedValue) SwitchResult {
	res := SwitchResult{
		FreezeVersion: r.freezeVersion,
		OldWatermark:  c.primary.Watermark(),
		NewWatermark:  r.New.Watermark(),
	}

	ready, missing := r.ReadyToSwitch()
	res.Missing = missing
	if !ready {
		res.Reason = fmt.Sprintf("新索引未追平冻结版本 %d（水位 %d，缺失版本 %v）",
			r.freezeVersion, r.New.Watermark(), missing)
		return res
	}

	discrepancies := Verify(r.New, reference)
	res.Discrepancies = discrepancies
	if len(discrepancies) > 0 {
		res.Reason = "新索引与全量扫描参考数据不一致，中止切换"
		return res
	}

	c.mu.Lock()
	c.primary = r.New
	c.mu.Unlock()
	res.Switched = true
	res.Reason = "新索引已追平冻结版本且与参考数据一致，提升为主索引"
	return res
}

// Verify 用全量扫描参考数据校验索引：缺键、多键、值不符、版本落后都算差异。
func Verify(ix *Index, reference map[string]VersionedValue) []string {
	live := ix.LiveEntries()
	var diffs []string

	for k, ref := range reference {
		got, ok := live[k]
		if !ok {
			diffs = append(diffs, fmt.Sprintf("键 %q 缺失（参考值=%q 版本=%d）", k, ref.Value, ref.Version))
			continue
		}
		if got.Value != ref.Value {
			diffs = append(diffs, fmt.Sprintf("键 %q 值不符（索引=%q 参考=%q）", k, got.Value, ref.Value))
		}
		if got.Version < ref.Version {
			diffs = append(diffs, fmt.Sprintf("键 %q 版本落后（索引=%d 参考=%d）", k, got.Version, ref.Version))
		}
	}
	for k, got := range live {
		if _, ok := reference[k]; !ok {
			diffs = append(diffs, fmt.Sprintf("键 %q 多余（索引值=%q 版本=%d，参考中不存在，疑似删除未生效）", k, got.Value, got.Version))
		}
	}
	sort.Strings(diffs)
	return diffs
}
