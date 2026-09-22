// Package sim 是一个单进程、确定性的离散事件模拟器：
// 通信（可丢包消息总线）、故障（带预算的故障点 -> panic 式崩溃/重启）
// 和时间（逻辑时钟 + 定时器堆）全部在这里被控制，因此同一种子的
// 两次运行完全一致，故障窗口可以被精确重放。
package sim

import (
	"container/heap"
	"fmt"
	"math/rand"
)

// Trace 记录一次可观测事件（发送、投递、丢弃、崩溃、重启、提交……）。
type Trace struct {
	Tick  int64
	Kind  string
	Node  string
	Label string
	Msg   *Msg
}

// crashFault 是故障点触发崩溃时抛出的哨兵 panic。
type crashFault struct {
	node  string
	label string
}

func (c crashFault) Error() string {
	return fmt.Sprintf("sim: node %q crashed at fault %q", c.node, c.label)
}

// Sim 是事件模拟器。
type Sim struct {
	Now   int64
	rng   *rand.Rand
	seq   int64
	queue eventHeap

	nodes map[string]*Node
	Bus   *Bus

	faults map[string]int // 故障标签 -> 剩余触发次数

	traces    []Trace
	invariant func(*Sim)
}

// New 创建模拟器，seed 决定所有抖动与随机选择。
func New(seed int64) *Sim {
	s := &Sim{
		rng:    rand.New(rand.NewSource(seed)),
		nodes:  map[string]*Node{},
		faults: map[string]int{},
	}
	s.Bus = newBus(s)
	return s
}

// RNG 暴露确定性随机源。
func (s *Sim) RNG() *rand.Rand { return s.rng }

// Traces 返回当前全部轨迹记录。
func (s *Sim) Traces() []Trace { return s.traces }

// RecordTrace 追加一条自定义轨迹（例如业务事务的提交点）。
func (s *Sim) RecordTrace(kind, node, label string) {
	s.trace(Trace{Kind: kind, Node: node, Label: label})
}

func (s *Sim) trace(t Trace) {
	t.Tick = s.Now
	s.traces = append(s.traces, t)
}

// Arm 在标签 label 上放置 budget 次故障。label 可以是完整标签，
// 也可以是前缀（如 "svc.before_commit" 命中 "svc.before_commit:c1"）。
func (s *Sim) Arm(label string, budget int) {
	s.faults[label] += budget
}

// FaultsLeft 返回某标签的剩余故障次数（精确匹配）。
func (s *Sim) FaultsLeft(label string) int { return s.faults[label] }

// consumeFault 选择匹配标签：完整匹配优先，否则取最长的已武装前缀。
// 命中且仍有预算时消耗一次并返回 true。
func (s *Sim) consumeFault(full string) (string, bool) {
	if b, ok := s.faults[full]; ok && b > 0 {
		s.faults[full] = b - 1
		return full, true
	}
	best := ""
	for label := range s.faults {
		if label == full || len(full) > len(label) && full[:len(label)] == label && full[len(label)] == ':' {
			if len(label) > len(best) && s.faults[label] > 0 {
				best = label
			}
		}
	}
	if best == "" {
		return "", false
	}
	s.faults[best]--
	return best, true
}

// SetInvariant 安装每事件后运行的安全不变量检查。检查中 panic 会
// 原样传播（测试直接失败），与崩溃哨兵区分开。
func (s *Sim) SetInvariant(fn func(*Sim)) { s.invariant = fn }

func (s *Sim) check() {
	if s.invariant != nil {
		s.invariant(s)
	}
}

// At 在绝对时刻 tick 调度回调。
func (s *Sim) At(tick int64, fn func()) {
	s.seq++
	heap.Push(&s.queue, &event{at: tick, seq: s.seq, run: fn})
}

// After 在 d 个逻辑时间单位后调度回调。
func (s *Sim) After(d int64, fn func()) { s.At(s.Now+d, fn) }

// RunUntil 推进事件，直到 done 返回 true（返回 true），
// 或处理事件数达到 cap（返回 false）。
func (s *Sim) RunUntil(done func(*Sim) bool, cap int) bool {
	for steps := 0; steps < cap; steps++ {
		if done(s) {
			return true
		}
		if s.queue.Len() == 0 {
			return done(s)
		}
		ev := heap.Pop(&s.queue).(*event)
		s.Now = ev.at
		s.runEvent(ev.run)
		s.check()
	}
	return done(s)
}

func (s *Sim) runEvent(fn func()) {
	defer func() {
		if r := recover(); r != nil {
			cf, ok := r.(crashFault)
			if !ok {
				panic(r)
			}
			n := s.nodes[cf.node]
			// 重启是一个新事件：重启前的状态对不变量必须仍然成立。
			s.After(restartDelay, func() { n.reboot(cf.label) })
		}
	}()
	fn()
}

// Node 是一个可崩溃的进程。
type Node struct {
	Name  string
	app   App
	sim   *Sim
	alive bool
	epoch int
}

// App 是节点上运行的应用；Start 在每次启动（含重启）时调用。
type App interface {
	Start(n *Node)
}

// AddNode 注册节点；返回后调用 Boot 启动。
func (s *Sim) AddNode(name string, app App) *Node {
	if _, dup := s.nodes[name]; dup {
		panic("sim: duplicate node " + name)
	}
	n := &Node{Name: name, app: app, sim: s}
	s.nodes[name] = n
	return n
}

// Node 按名查找节点。
func (s *Sim) Node(name string) *Node { return s.nodes[name] }

// Alive 报告节点是否存活。
func (n *Node) Alive() bool { return n.alive }

// Epoch 返回启动代次，每次崩溃重启加一。
func (n *Node) Epoch() int { return n.epoch }

// After 调度一个仅在当前代次有效的定时器；节点崩溃后旧定时器自动作废。
func (n *Node) After(d int64, fn func()) {
	epoch := n.epoch
	n.sim.After(d, func() {
		if n.epoch != epoch {
			n.sim.trace(Trace{Kind: "timer-stale", Node: n.Name})
			return
		}
		fn()
	})
}

// Fault 在节点上触发故障点：记录命中轨迹，若该标签已武装则崩溃。
// 崩溃通过 panic 跳过该事件回调的剩余代码（包括后续发送）。
func (n *Node) Fault(label string) {
	full := n.Name + "." + label
	matched, armed := n.sim.consumeFault(full)
	n.sim.trace(Trace{Kind: "fault", Node: n.Name, Label: full})
	if !armed {
		return
	}
	n.crash(matched)
}

func (n *Node) crash(matched string) {
	n.alive = false
	n.sim.trace(Trace{Kind: "crash", Node: n.Name, Label: matched})
	panic(crashFault{node: n.Name, label: matched})
}

func (n *Node) reboot(crashLabel string) {
	n.alive = true
	n.epoch++
	n.sim.trace(Trace{Kind: "reboot", Node: n.Name, Label: crashLabel})
	n.app.Start(n)
}

// Boot 首次启动节点。
func (n *Node) Boot() {
	n.alive = true
	n.epoch = 1
	n.sim.trace(Trace{Kind: "boot", Node: n.Name})
	n.app.Start(n)
}

const restartDelay int64 = 12
