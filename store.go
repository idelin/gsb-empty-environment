package main

import (
	"fmt"
	"sync"
	"time"
)

// Value 一个带因果版本的值；Deleted 为 true 表示墓碑
type Value struct {
	Clock   VClock
	Data    string
	Deleted bool
	Writer  string
}

func (v Value) Copy() Value {
	v.Clock = v.Clock.Copy()
	return v
}

func (v Value) String() string {
	if v.Deleted {
		return fmt.Sprintf("TOMBSTONE@%v(by %s)", v.Clock, v.Writer)
	}
	return fmt.Sprintf("%q@%v(by %s)", v.Data, v.Clock, v.Writer)
}

// MergeValues 归并一组版本，只保留极大元（不被任何其他版本支配的版本）。
// 并发分支全部保留为兄弟版本；被支配的旧版本（含旧值与旧墓碑）被丢弃。
func MergeValues(vals []Value) []Value {
	var out []Value
	for _, v := range vals {
		dominated := false
		kept := out[:0]
		for _, k := range out {
			switch v.Clock.Compare(k.Clock) {
			case Greater: // 新版本支配旧版本，丢弃旧的
				continue
			case Less, Equal: // 被已有版本支配或重复
				dominated = true
			}
			kept = append(kept, k)
		}
		out = kept
		if !dominated {
			out = append(out, v.Copy())
		}
	}
	return out
}

// sameSet 判断两个版本集合是否等价（用于判断修复是否产生变化）
func sameSet(a, b []Value) bool {
	if len(a) != len(b) {
		return false
	}
	for _, x := range a {
		found := false
		for _, y := range b {
			if x.Clock.Compare(y.Clock) == Equal && x.Deleted == y.Deleted && x.Data == y.Data {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// Node 一个副本节点，保存每个 key 的兄弟版本集合
type Node struct {
	id      string
	latency time.Duration // 模拟网络延迟（迟到响应）
	mu      sync.Mutex
	data    map[string][]Value
}

func NewNode(id string, latency time.Duration) *Node {
	return &Node{id: id, latency: latency, data: make(map[string][]Value)}
}

// get 读取本地版本集合（深拷贝）
func (n *Node) get(key string) []Value {
	n.mu.Lock()
	defer n.mu.Unlock()
	var out []Value
	for _, v := range n.data[key] {
		out = append(out, v.Copy())
	}
	return out
}

// writeLocal 本地写入（模拟客户端写落到单个副本）
func (n *Node) writeLocal(key, data string, deleted bool, writer string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	// 基于本地已有版本推进时钟；并发写自然产生并发时钟
	clock := VClock{}
	for _, v := range n.data[key] {
		for k, c := range v.Clock {
			if c > clock[k] {
				clock[k] = c
			}
		}
	}
	clock.Increment(writer)
	n.data[key] = []Value{{Clock: clock, Data: data, Deleted: deleted, Writer: writer}}
}

// repair 条件修复：把读到的归并结果原子地与本地当前状态归并。
// 由于整个归并在锁内完成，修复发送后目标节点即便已被并发更新，
// 也不会被旧值覆盖——并发分支会被保留为兄弟版本，墓碑也不会被旧值复活。
// 返回本地状态是否发生变化。
func (n *Node) repair(key string, incoming []Value) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	local := n.data[key]
	merged := MergeValues(append(append([]Value{}, local...), incoming...))
	if sameSet(local, merged) {
		return false // 本地已不落后于修复内容，无需变更
	}
	n.data[key] = merged
	return true
}

// ReadResult 读协调结果
type ReadResult struct {
	Siblings []Value // 法定人数响应归并后的全部并发分支
	Winner   Value   // 返回给客户端的胜出版本（确定性决胜）
}

// Cluster 副本集群，负责多读归并与异步读修复
type Cluster struct {
	nodes []*Node
	r     int // 法定人数
	repMu sync.Mutex
	repWg sync.WaitGroup
	// 记录每次修复，便于测试与演示观察
	repairLog []string
}

func NewCluster(r int, nodes ...*Node) *Cluster {
	return &Cluster{nodes: nodes, r: r}
}

// pickWinner 在兄弟版本中确定性地选出胜出版本：
// 时钟分量和大者优先，其次按 Writer 字典序，保证所有协调者选法一致。
func pickWinner(siblings []Value) Value {
	best := siblings[0]
	for _, v := range siblings[1:] {
		bs, vs := best.Clock.Sum(), v.Clock.Sum()
		if vs > bs || (vs == bs && v.Writer > best.Writer) {
			best = v
		}
	}
	return best
}

// Read 从全部副本并发读取，收齐法定人数后归并返回；
// 迟到响应继续收集，并入修复内容后对所有副本做异步条件修复。
func (c *Cluster) Read(key string) ReadResult {
	ch := make(chan []Value, len(c.nodes))
	for _, n := range c.nodes {
		go func(n *Node) {
			time.Sleep(n.latency) // 模拟网络延迟，慢节点产生迟到响应
			ch <- n.get(key)
		}(n)
	}

	var quorum []Value
	for i := 0; i < c.r; i++ {
		quorum = append(quorum, (<-ch)...)
	}
	siblings := MergeValues(quorum)

	// 异步修复：等待迟到响应，归并全部版本后条件修复每个副本
	c.repWg.Add(1)
	go func() {
		defer c.repWg.Done()
		all := append([]Value{}, quorum...)
		for i := c.r; i < len(c.nodes); i++ {
			all = append(all, (<-ch)...) // 迟到分支不丢：并入修复内容
		}
		merged := MergeValues(all)
		for _, n := range c.nodes {
			if n.repair(key, merged) {
				c.repMu.Lock()
				c.repairLog = append(c.repairLog,
					fmt.Sprintf("repaired %s <- %v", n.id, merged))
				c.repMu.Unlock()
			}
		}
	}()

	if len(siblings) == 0 {
		return ReadResult{}
	}
	return ReadResult{Siblings: siblings, Winner: pickWinner(siblings)}
}

// WaitRepair 等待所有异步修复完成（测试与演示用）
func (c *Cluster) WaitRepair() {
	c.repWg.Wait()
}

// RepairLog 返回修复日志
func (c *Cluster) RepairLog() []string {
	c.repMu.Lock()
	defer c.repMu.Unlock()
	return append([]string{}, c.repairLog...)
}

// Versions 查看某节点当前保存的版本集合
func (c *Cluster) Versions(nodeIdx int, key string) []Value {
	return c.nodes[nodeIdx].get(key)
}
