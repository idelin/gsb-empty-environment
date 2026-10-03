// Package paxos 实现 Multi-Paxos 多数派共识日志。
//
// 设计要点：
//   - Prepare/Promise 与 Accept/Accepted 两阶段协议；
//   - 领导者持有租约（lease）时跳过 Phase 1，直接 Phase 2 连续提交日志；
//   - Phase 1 会修补空洞槽位（noop 填充）并覆写少数派脏日志；
//   - 接受者状态全部基于原子操作与无锁数据结构（无全局互斥锁）：
//     promised 用 atomic.Uint64 CAS 单调推进，日志槽用不可变快照 + 指针 CAS，
//     已提交事件经由 lockfree.Queue 异步按序应用。
package paxos

import (
	"bytes"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"multipaxos/lockfree"
)

// ErrNoQuorum 表示无法联系到多数派（例如网络分区中的少数派一侧）。
var ErrNoQuorum = errors.New("paxos: no quorum")

// AppliedEntry 是一条已按日志顺序应用的条目。Value 为 nil 表示 noop（补洞条目）。
type AppliedEntry struct {
	Slot  uint64
	Value []byte
}

type commitEvent struct {
	slot  uint64
	value []byte
}

// Node 是集群中的一个 Multi-Paxos 节点，同时承担 acceptor 与（潜在）leader 角色。
type Node struct {
	id       int
	peers    []int
	majority int
	nw       *Network
	leaseDur time.Duration

	promised atomic.Uint64 // 已承诺的最大提案号（Ballot），CAS 单调推进
	log      *slotLog      // 无锁日志槽

	// 领导者状态（仅本节点为 leader 时有意义）
	ballot   atomic.Uint64 // 当前领导任期使用的提案号
	nextSlot atomic.Uint64 // 下一个待分配的日志槽位
	leaseExp atomic.Int64  // 租约到期时间（UnixNano）

	commitFrontier atomic.Uint64 // 已连续应用的槽位数（= 下一个待应用槽位）

	commitQ  *lockfree.Queue[commitEvent]  // 已提交、待按序应用的事件
	appliedQ *lockfree.Queue[AppliedEntry] // 已按序应用的条目（供上层消费）

	notify chan struct{}
	done   chan struct{}
	closed atomic.Bool
	wg     sync.WaitGroup // 仅用于等待 applyLoop 退出，不在热路径上
}

// NewNode 创建节点并启动后台应用循环。leaseDur 为领导者租约时长。
func NewNode(id int, nw *Network, leaseDur time.Duration) *Node {
	n := &Node{
		id:       id,
		peers:    nw.IDs(),
		majority: nw.Size()/2 + 1,
		nw:       nw,
		leaseDur: leaseDur,
		log:      newSlotLog(),
		commitQ:  lockfree.NewQueue[commitEvent](),
		appliedQ: lockfree.NewQueue[AppliedEntry](),
		notify:   make(chan struct{}, 1),
		done:     make(chan struct{}),
	}
	n.wg.Add(1)
	go n.applyLoop()
	return n
}

// ID 返回节点标识。
func (n *Node) ID() int { return n.id }

// Applied 返回已应用条目的无锁队列，调用方通过 Dequeue 消费。
func (n *Node) Applied() *lockfree.Queue[AppliedEntry] { return n.appliedQ }

// CommitFrontier 返回已连续应用的槽位数。
func (n *Node) CommitFrontier() uint64 { return n.commitFrontier.Load() }

// Close 停止后台应用循环并等待其退出，保证无 goroutine 泄漏。
func (n *Node) Close() {
	if n.closed.CompareAndSwap(false, true) {
		close(n.done)
		n.wg.Wait()
	}
}

// ---------------- Acceptor：Phase 1 ----------------

// HandlePrepare 处理 Phase 1a。若提案号足够大，则原子地推进 promised
// 并汇报本地 >= FromSlot 的全部已接受/已提交条目。
func (n *Node) HandlePrepare(req PrepareRequest) PrepareResponse {
	for {
		cur := Ballot(n.promised.Load())
		if req.Ballot <= cur {
			return PrepareResponse{OK: false, Promised: cur}
		}
		if n.promised.CompareAndSwap(uint64(cur), uint64(req.Ballot)) {
			break
		}
	}
	var entries []SlotEntry
	max := n.log.maxIndex()
	for i := req.FromSlot; i <= uint64(max) && max >= 0; i++ {
		if s := n.log.load(i); s != nil && s.hasValue {
			entries = append(entries, SlotEntry{
				Slot:      i,
				Ballot:    s.acceptedBallot,
				Value:     s.value,
				Committed: s.committed,
			})
		}
	}
	return PrepareResponse{OK: true, Promised: req.Ballot, Entries: entries}
}

// ---------------- Acceptor：Phase 2 ----------------

// HandleAccept 处理 Phase 2a。提案号低于 promised 时拒绝；
// 已提交槽位幂等接受同值、拒绝异值（安全不变量）。
func (n *Node) HandleAccept(req AcceptRequest) AcceptResponse {
	if req.Ballot < Ballot(n.promised.Load()) {
		return AcceptResponse{OK: false, Promised: Ballot(n.promised.Load())}
	}
	p := n.log.slot(req.Slot)
	for {
		old := p.Load()
		if old != nil && old.committed {
			if old.hasValue && !bytes.Equal(old.value, req.Value) {
				// 已提交的值绝不可被覆写
				return AcceptResponse{OK: false, Promised: Ballot(n.promised.Load())}
			}
			return AcceptResponse{OK: true} // 幂等
		}
		ns := &slotState{acceptedBallot: req.Ballot, value: req.Value, hasValue: true}
		if p.CompareAndSwap(old, ns) {
			return AcceptResponse{OK: true}
		}
	}
}

// HandleCommit 处理提交通知。提交是权威性的：
// 未提交的（脏）已接受值会被安全覆写；已提交的同值请求幂等；
// 已提交的异值请求意味着安全违规，直接 panic 以便测试暴露。
func (n *Node) HandleCommit(msg CommitMessage) {
	// 看到更高提案号的提交，说明已有新领导者取得多数派：本节点让位。
	if uint64(msg.Ballot) > n.ballot.Load() {
		n.ballot.Store(0)
		n.leaseExp.Store(0)
	}
	// promised 单调跟进，避免旧领导者用低提案号继续写入。
	for {
		cur := n.promised.Load()
		if uint64(msg.Ballot) <= cur {
			break
		}
		if n.promised.CompareAndSwap(cur, uint64(msg.Ballot)) {
			break
		}
	}
	p := n.log.slot(msg.Slot)
	for {
		old := p.Load()
		if old != nil && old.committed {
			if !bytes.Equal(old.value, msg.Value) {
				panic("paxos safety violation: conflicting committed values")
			}
			return // 幂等
		}
		ns := &slotState{
			acceptedBallot: msg.Ballot,
			value:          msg.Value,
			hasValue:       true,
			committed:      true,
		}
		if p.CompareAndSwap(old, ns) {
			break
		}
	}
	n.commitQ.Enqueue(commitEvent{slot: msg.Slot, value: msg.Value})
	n.signal()
}

// ---------------- 有序应用 ----------------

func (n *Node) signal() {
	select {
	case n.notify <- struct{}{}:
	default:
	}
}

// applyLoop 是唯一的应用 goroutine：从无锁队列取出提交事件，
// 按槽位顺序应用（空洞会阻塞后续条目，直到被修补）。
func (n *Node) applyLoop() {
	defer n.wg.Done()
	pending := make(map[uint64][]byte)
	next := uint64(0)
	drain := func() {
		for {
			ev, ok := n.commitQ.Dequeue()
			if !ok {
				break
			}
			if ev.slot >= next {
				pending[ev.slot] = ev.value
			}
		}
		for {
			v, ok := pending[next]
			if !ok {
				break
			}
			delete(pending, next)
			n.appliedQ.Enqueue(AppliedEntry{Slot: next, Value: v})
			next++
			n.commitFrontier.Store(next)
		}
	}
	for {
		drain()
		select {
		case <-n.done:
			drain() // 退出前收尾，避免丢失已提交事件
			return
		case <-n.notify:
		}
	}
}
