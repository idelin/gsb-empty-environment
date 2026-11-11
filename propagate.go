package netsec

import "sort"

// Hop 描述传播路径上的一跳：事件从 From 节点经 Mechanism 到达 To 节点。
type Hop struct {
	// From 是跳的起点节点 ID（首跳为事件源节点）。
	From string
	// To 是跳的终点节点 ID。
	To string
	// Mechanism 是本跳使用的传播机制。
	Mechanism Mechanism
	// Group 是参与本跳的传播组 ID；私有节点出边为空串。
	Group string
}

// Mechanism 标识一跳的传播机制。
type Mechanism int

const (
	// HopSharedPeer 表示共享组内的同伴传播（双向，但每一跳都有确定方向）。
	HopSharedPeer Mechanism = iota + 1
	// HopDownstreamSlave 表示从共享组或上游从属组流入下游从属组，只允许向下。
	HopDownstreamSlave
)

// String 返回传播机制的可读名称。
func (m Mechanism) String() string {
	switch m {
	case HopSharedPeer:
		return "shared-peer"
	case HopDownstreamSlave:
		return "downstream-slave"
	default:
		return "unknown"
	}
}

// Reached 记录一个被传播到的节点，以及从事件源到该节点的一条最短路径。
type Reached struct {
	// NodeID 是被影响的挂载节点 ID。
	NodeID string
	// Namespace 是该节点所在的挂载命名空间。
	Namespace string
	// Path 是从事件源到该节点的跳序列；事件源自身的 Path 为空。
	Path []Hop
}

// Report 是一次挂载事件的传播结果。
type Report struct {
	// Origin 是发起事件的节点 ID。
	Origin string
	// Reached 是所有受影响节点（含事件源），按节点 ID 排序。
	Reached []Reached
	// Namespaces 是所有受影响命名空间的去重有序列表（含事件源所在命名空间）。
	Namespaces []string
	// HostVisible 表示传播是否抵达构造 Graph 时配置的宿主命名空间。
	HostVisible bool
}

// Propagate 从 origin 节点重放一次挂载事件，返回完整传播报告。

// 传播规则（与 Linux mount propagation 对齐）：
//   - Private 节点是传播终点，事件不外泄；
//   - Shared 节点把事件发给同组所有共享同伴，并继续流入以该组为主组的下游从属组；
//   - Slave 节点只在自己的从属组内继续向下游从属组转发，绝不回灌主组
//     （因此反向传播在结构上不可能发生）。
//
// 图是有限的，组从属关系保持有向无环，BFS 配合 visited 集合必然终止；
// 每个 Reached 保留 BFS 首次到达时的最短传播路径。
func (g *Graph) Propagate(origin string) *Report {
	report := &Report{Origin: origin}
	src, ok := g.nodes[origin]
	if !ok {
		return report
	}

	visited := map[string]bool{origin: true}
	paths := map[string][]Hop{origin: nil}
	queue := []string{origin}
	nsSeen := map[string]bool{src.Namespace: true}

	for len(queue) > 0 {
		curID := queue[0]
		queue = queue[1:]
		curPath := paths[curID]

		for _, nextID := range g.outbound(curID) {
			if visited[nextID] {
				continue
			}
			visited[nextID] = true
			hop := g.buildHop(curID, nextID)
			paths[nextID] = append(append([]Hop(nil), curPath...), hop)
			queue = append(queue, nextID)
			nsSeen[g.nodes[nextID].Namespace] = true
		}
	}

	nodeIDs := make([]string, 0, len(visited))
	for id := range visited {
		nodeIDs = append(nodeIDs, id)
	}
	sort.Strings(nodeIDs)
	for _, id := range nodeIDs {
		report.Reached = append(report.Reached, Reached{
			NodeID:    id,
			Namespace: g.nodes[id].Namespace,
			Path:      paths[id],
		})
	}

	namespaces := make([]string, 0, len(nsSeen))
	for ns := range nsSeen {
		namespaces = append(namespaces, ns)
	}
	sort.Strings(namespaces)
	report.Namespaces = namespaces

	if g.hostNamespace != "" && nsSeen[g.hostNamespace] {
		report.HostVisible = true
	}
	return report
}

// HostReachable 是控制面常用的便捷判定：从 origin 发起的更新是否会出现在宿主。
func (g *Graph) HostReachable(origin string) bool {
	return g.Propagate(origin).HostVisible
}

// outbound 返回从节点 id 出发可在一跳内到达的全部节点 ID（有序，保证确定性）。
func (g *Graph) outbound(id string) []string {
	node := g.nodes[id]
	seen := map[string]struct{}{}

	switch node.Type {
	case Private:
		return nil
	case Shared:
		grp := g.groups[node.Group]
		for member := range grp.members {
			if member != id {
				seen[member] = struct{}{}
			}
		}
		g.appendSlaveMembers(grp.id, seen)
	case Slave:
		// 从属节点不向主组回灌，只沿自己所属从属组继续向下游转发。
		g.appendSlaveMembers(node.Group, seen)
	}

	out := make([]string, 0, len(seen))
	for next := range seen {
		out = append(out, next)
	}
	sort.Strings(out)
	return out
}

// appendSlaveMembers 把所有以 masterGroup 为主组的下游从属组成员加入 seen。
func (g *Graph) appendSlaveMembers(masterGroup string, seen map[string]struct{}) {
	slaveGroupIDs := make([]string, 0)
	for gid, grp := range g.groups {
		if grp.kind == Slave && grp.master == masterGroup {
			slaveGroupIDs = append(slaveGroupIDs, gid)
		}
	}
	sort.Strings(slaveGroupIDs)
	for _, gid := range slaveGroupIDs {
		for member := range g.groups[gid].members {
			seen[member] = struct{}{}
		}
	}
}

// buildHop 还原 from -> to 这一跳的传播机制与所属组。
func (g *Graph) buildHop(from, to string) Hop {
	fromNode := g.nodes[from]
	toNode := g.nodes[to]

	if fromNode.Type == Shared && toNode.Type == Shared && fromNode.Group == toNode.Group {
		return Hop{From: from, To: to, Mechanism: HopSharedPeer, Group: fromNode.Group}
	}

	// 其余可达边均为“主组 -> 下游从属组”，组 ID 取终点节点的从属组。
	return Hop{From: from, To: to, Mechanism: HopDownstreamSlave, Group: toNode.Group}
}
