package repartition

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
)

// 哨兵错误。
var (
	// ErrClosed 表示屏障已关闭或在故障注入后被中毒丢弃。
	ErrClosed = errors.New("repartition: 屏障已关闭")
	// ErrFenced 表示拓扑切换完成后仍收到旧拓扑记录，旧拓扑已被隔离。
	ErrFenced = errors.New("repartition: 旧拓扑已隔离，记录被拒绝")
	// ErrOffsetOrder 表示同一分区内出现偏移回退或重复偏移。
	ErrOffsetOrder = errors.New("repartition: 分区偏移非单调递增")
	// ErrConfigMismatch 表示用与上次持久化不一致的配置重新打开屏障。
	ErrConfigMismatch = errors.New("repartition: 屏障配置与持久化状态不一致")
	// ErrDone 表示当前没有待处理记录（稍后可能还会有新记录）。
	ErrDone = errors.New("repartition: 没有待处理记录")
)

// Record 是来自采集端的一条数据记录。
//
// Topology 为 true 表示新拓扑分区的记录，false 表示旧拓扑分区。
// Partition/Offset 唯一确定一条记录，也是幂等去重的依据。
type Record struct {
	Topology  bool   `json:"topology"`
	Partition int    `json:"partition"`
	Offset    int64  `json:"offset"`
	Key       string `json:"key"`
	Payload   []byte `json:"payload,omitempty"`
	// seq 是组件内部分配的持久化单调序号，用于崩溃恢复后保持投递顺序。
	seq int64 `json:"-"`
}

func (r Record) id() string {
	return fmt.Sprintf("%t:%d:%d", r.Topology, r.Partition, r.Offset)
}

// Config 描述一次重分区：旧分区集合、新分区集合，以及每个旧分区的切换偏移。
// 仅当所有旧分区都在各自的 CutOffsets 处收到切换通知后，新拓扑才原子启用。
type Config struct {
	OldPartitions []int         `json:"old_partitions"`
	NewPartitions []int         `json:"new_partitions"`
	CutOffsets    map[int]int64 `json:"cut_offsets"`
}

func (c Config) normalized() Config {
	out := Config{
		OldPartitions: append([]int(nil), c.OldPartitions...),
		NewPartitions: append([]int(nil), c.NewPartitions...),
		CutOffsets:    make(map[int]int64, len(c.CutOffsets)),
	}
	sort.Ints(out.OldPartitions)
	sort.Ints(out.NewPartitions)
	for k, v := range c.CutOffsets {
		out.CutOffsets[k] = v
	}
	return out
}

func (c Config) validate() error {
	if len(c.OldPartitions) == 0 {
		return errors.New("repartition: 旧分区集合不能为空")
	}
	if len(c.NewPartitions) == 0 {
		return errors.New("repartition: 新分区集合不能为空")
	}
	seen := map[int]bool{}
	for _, p := range c.OldPartitions {
		if seen[p] {
			return fmt.Errorf("repartition: 旧分区 %d 重复", p)
		}
		seen[p] = true
		cut, ok := c.CutOffsets[p]
		if !ok {
			return fmt.Errorf("repartition: 旧分区 %d 缺少切换偏移", p)
		}
		if cut < 0 {
			return fmt.Errorf("repartition: 旧分区 %d 切换偏移非法", p)
		}
	}
	nseen := map[int]bool{}
	for _, p := range c.NewPartitions {
		if nseen[p] {
			return fmt.Errorf("repartition: 新分区 %d 重复", p)
		}
		nseen[p] = true
	}
	return nil
}

// Sink 是下游发布接口。实现必须以 Record 的分区+偏移为幂等键：
// 组件保证“至少一次”调用，崩溃恢复后可能重复调用同一条记录，
// Sink 需保证重复调用不产生重复副作用。
type Sink interface {
	Publish(ctx context.Context, r Record) error
}

// SinkFunc 让普通函数满足 Sink 接口。
type SinkFunc func(ctx context.Context, r Record) error

// Publish 实现 Sink。
func (f SinkFunc) Publish(ctx context.Context, r Record) error { return f(ctx, r) }

// FaultConfig 配置一次写盘故障注入，用于测试崩溃恢复。
//
// AtAppend 是逻辑追加序号（从 1 开始，重复通知不会占用序号）。
// BeforeSync=true 时只写半帧且不 fsync，模拟断电产生的半写尾部；
// BeforeSync=false 时完整 fsync 后再“崩溃”，模拟提交后崩溃。
// 故障触发后屏障中毒，后续操作返回注入错误，调用方应重新 Open。
type FaultConfig struct {
	AtAppend   int
	BeforeSync bool
	Err        error
}

// Option 配置屏障。
type Option func(*Barrier)

// WithFault 注入一次写盘故障。仅用于测试。
func WithFault(f FaultConfig) Option {
	return func(b *Barrier) { b.fault = &f }
}

// 持久化事件类型。
const (
	evConfig    = "config"
	evRecord    = "record"
	evCutover   = "cutover"
	evSwitched  = "switched"
	evDelivered = "delivered"
	evCommitted = "committed"
)

type cutoverEvent struct {
	Partition int   `json:"partition"`
	Offset    int64 `json:"offset"`
}

type commitEvent struct {
	ID string `json:"id"`
}

// recordEnvelope 给 Record 附上持久化单调序号。
type recordEnvelope struct {
	Record
	Seq int64 `json:"seq"`
}

// Barrier 是可嵌入 Go 消费者的重分区屏障。
//
// 所有状态（屏障配置、记录、切换通知、拓扑翻转、下游回执）都以事件形式
// 先写 WAL 再更新内存，因此任何一次成功写盘之后崩溃，重启都能完整恢复；
// 重复的记录/通知/回执调用是幂等的。
type Barrier struct {
	mu     sync.Mutex
	cond   *sync.Cond
	wal    *wal
	cfg    Config
	fault  *FaultConfig
	closed bool

	// 旧拓扑：分区 -> 已接收的最大偏移（单调性与去重校验）。
	oldMax map[int]int64
	// 旧拓扑待发布队列，按 (分区, 偏移) 全局有序。
	oldQueue []Record

	// 新拓扑：分区 -> 已接收的最大偏移。
	newMax map[int]int64
	// 新拓扑待发布记录的最小堆。
	newHeap recordHeap

	// 已到达切换偏移的旧分区。
	cutReached map[int]bool
	// 拓扑是否已原子翻转。
	switched bool

	// 已持久化下游回执的记录 ID。
	committed map[string]bool
	// 已持久化“已投递”标记、等待下游回执的记录 ID。
	delivered map[string]bool
	// 待发布（可见但尚未拿到回执）的记录，按投递顺序排列。
	pending []Record
	// pending 中记录 ID 的集合，保证任何记录只入队一次。
	pendingIDs map[string]bool

	// 所有曾接收过的记录 ID，用于幂等去重。
	known map[string]bool

	// 下一条记录的持久化序号。
	nextSeq int64
}
