package netsec

import (
	"fmt"
	"sort"
)

// Stamp 是事件的全序时间戳：先比较墙上时间，再比较同刻事件顺序号。
// 同一时刻发生的事件由 Seq 给出确定的先后关系。
type Stamp struct {
	Time int64
	Seq  uint64
}

// Compare 返回 -1/0/1，表示 a 早于/等于/晚于 b。
func (a Stamp) Compare(b Stamp) int {
	switch {
	case a.Time < b.Time:
		return -1
	case a.Time > b.Time:
		return 1
	case a.Seq < b.Seq:
		return -1
	case a.Seq > b.Seq:
		return 1
	default:
		return 0
	}
}

// ProcessRef 通过 (进程号, 启动ID) 唯一标识一次进程生命期。
// 启动ID 在系统范围内单调分配，进程号被操作系统重用也不会
// 把前后两个不同的进程混为同一节点。
type ProcessRef struct {
	PID     int32
	StartID uint64
}

// NodeKind 区分溯源图中的节点类型。
type NodeKind uint8

const (
	NodeProcess NodeKind = iota + 1
	NodeFile
	NodeSocket
)

// Node 是溯源图节点：进程按启动ID建点，文件按内容版本建点，
// 网络对端按地址建点。
type Node struct {
	Kind NodeKind

	// NodeProcess
	PID     int32
	StartID uint64

	// NodeFile
	Path    string
	Version uint64

	// NodeSocket
	Addr string
}

// ProcessNode 构造进程节点。
func ProcessNode(p ProcessRef) Node {
	return Node{Kind: NodeProcess, PID: p.PID, StartID: p.StartID}
}

// FileNode 构造文件的某个内容版本节点。
func FileNode(path string, version uint64) Node {
	return Node{Kind: NodeFile, Path: path, Version: version}
}

// SocketNode 构造网络对端节点。
func SocketNode(addr string) Node {
	return Node{Kind: NodeSocket, Addr: addr}
}

// ID 返回节点的规范标识。
func (n Node) ID() string {
	switch n.Kind {
	case NodeProcess:
		return fmt.Sprintf("proc:%d:%d", n.PID, n.StartID)
	case NodeFile:
		return fmt.Sprintf("file:%s#v%d", n.Path, n.Version)
	case NodeSocket:
		return fmt.Sprintf("sock:%s", n.Addr)
	default:
		return "unknown"
	}
}

func (n Node) String() string { return n.ID() }

// EdgeKind 区分带时间的因果边类型。
type EdgeKind uint8

const (
	EdgeRead    EdgeKind = iota + 1 // 文件版本 -> 进程
	EdgeWrite                       // 进程 -> 文件新版本
	EdgeFork                        // 父进程 -> 子进程
	EdgeConnect                     // 进程 -> 网络对端
)

// Edge 是一条带时间的因果边，方向与信息流方向一致。
type Edge struct {
	Kind  EdgeKind
	From  Node
	To    Node
	Stamp Stamp
}

// EventKind 区分可重放的审计事件类型。
type EventKind uint8

const (
	EventRead EventKind = iota + 1
	EventWrite
	EventFork
	EventConnect
)

// Event 是一条审计事件。按事件类型使用相应字段：
//   - EventRead/EventWrite: Proc + Path
//   - EventFork:            Proc + Child
//   - EventConnect:         Proc + Addr
type Event struct {
	Kind EventKind
	Time int64
	Seq  uint64

	Proc  ProcessRef
	Path  string
	Child ProcessRef
	Addr  string
}

// Graph 是溯源图。进程节点以 (PID, StartID) 区分生命期，
// 文件节点以 (路径, 内容版本) 区分：每次写入产生新版本，
// 读取绑定到读取时刻的当前版本，因此覆盖写入后旧内容自然
// 与后续事件解耦。
type Graph struct {
	nodes    map[string]Node
	edges    []Edge
	inEdges  map[string][]int
	outEdges map[string][]int
	version  map[string]uint64
}

// NewGraph 创建空溯源图。
func NewGraph() *Graph {
	return &Graph{
		nodes:    make(map[string]Node),
		inEdges:  make(map[string][]int),
		outEdges: make(map[string][]int),
		version:  make(map[string]uint64),
	}
}

func (g *Graph) addNode(n Node) {
	if _, ok := g.nodes[n.ID()]; !ok {
		g.nodes[n.ID()] = n
	}
}

func (g *Graph) addEdge(kind EdgeKind, from, to Node, s Stamp) int {
	g.addNode(from)
	g.addNode(to)
	idx := len(g.edges)
	g.edges = append(g.edges, Edge{Kind: kind, From: from, To: to, Stamp: s})
	g.outEdges[from.ID()] = append(g.outEdges[from.ID()], idx)
	g.inEdges[to.ID()] = append(g.inEdges[to.ID()], idx)
	return idx
}

// Apply 摄入单条事件，返回新增因果边的句柄（可用于 Backtrace）。
func (g *Graph) Apply(ev Event) int {
	stamp := Stamp{Time: ev.Time, Seq: ev.Seq}
	proc := ProcessNode(ev.Proc)
	switch ev.Kind {
	case EventRead:
		file := FileNode(ev.Path, g.version[ev.Path])
		return g.addEdge(EdgeRead, file, proc, stamp)
	case EventWrite:
		g.version[ev.Path]++
		file := FileNode(ev.Path, g.version[ev.Path])
		return g.addEdge(EdgeWrite, proc, file, stamp)
	case EventFork:
		return g.addEdge(EdgeFork, proc, ProcessNode(ev.Child), stamp)
	case EventConnect:
		return g.addEdge(EdgeConnect, proc, SocketNode(ev.Addr), stamp)
	default:
		return -1
	}
}

// Replay 按 (Time, Seq) 全序重放事件流，返回与输入一一对应的边句柄。
// 输入无需有序，同刻事件按顺序号确定先后。
func (g *Graph) Replay(events []Event) []int {
	type indexed struct {
		ev Event
		i  int
	}
	sorted := make([]indexed, len(events))
	for i, ev := range events {
		sorted[i] = indexed{ev: ev, i: i}
	}
	sort.SliceStable(sorted, func(a, b int) bool {
		x := Stamp{Time: sorted[a].ev.Time, Seq: sorted[a].ev.Seq}
		y := Stamp{Time: sorted[b].ev.Time, Seq: sorted[b].ev.Seq}
		return x.Compare(y) < 0
	})
	handles := make([]int, len(events))
	for _, it := range sorted {
		handles[it.i] = g.Apply(it.ev)
	}
	return handles
}

// Edge 按句柄返回边。
func (g *Graph) Edge(handle int) Edge { return g.edges[handle] }
