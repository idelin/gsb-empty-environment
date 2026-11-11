package netsec

import (
	"fmt"
	"sort"
)

// PropType 标记一个挂载节点的传播类型，对应 Linux mount propagation 语义。
type PropType int

const (
	// Private 私有挂载：挂载事件既不接收也不向外传播。
	Private PropType = iota + 1
	// Shared 共享挂载：事件在同组共享同伴之间双向传播，并向其下游从属组传播。
	Shared
	// Slave 从属挂载：只从主组单向接收事件，再向自己的下游从属组传播。
	Slave
)

// String 返回传播类型的可读名称。
func (t PropType) String() string {
	switch t {
	case Private:
		return "private"
	case Shared:
		return "shared"
	case Slave:
		return "slave"
	default:
		return fmt.Sprintf("proptype(%d)", int(t))
	}
}

// 调用方可以用 errors.Is 判定的哨兵错误。
var (
	// ErrInvalidArgument 表示入参为空或自相矛盾。
	ErrInvalidArgument = fmt.Errorf("invalid argument")
	// ErrNodeNotFound 表示引用了不存在的挂载节点。
	ErrNodeNotFound = fmt.Errorf("mount node not found")
	// ErrNodeExists 表示节点 ID 已存在。
	ErrNodeExists = fmt.Errorf("mount node already exists")
	// ErrGroupNotFound 表示引用了不存在的传播组。
	ErrGroupNotFound = fmt.Errorf("propagation group not found")
	// ErrGroupExists 表示传播组 ID 已存在。
	ErrGroupExists = fmt.Errorf("propagation group already exists")
	// ErrInvalidPropType 表示传播类型取值非法，或在共享组上执行了仅从属组允许的操作。
	ErrInvalidPropType = fmt.Errorf("invalid propagation type")
	// ErrGroupKindMismatch 表示节点加入了与其传播类型不匹配的组。
	ErrGroupKindMismatch = fmt.Errorf("group kind does not match node propagation type")
	// ErrParentNamespace 表示挂载与其父挂载不在同一命名空间。
	ErrParentNamespace = fmt.Errorf("parent mount lives in a different namespace")
	// ErrHasChildren 表示不能删除仍有子挂载的节点。
	ErrHasChildren = fmt.Errorf("mount still has children")
	// ErrMoveToDescendant 表示移动目标是被移动挂载自身或其后代。
	ErrMoveToDescendant = fmt.Errorf("cannot move a mount under itself")
	// ErrCannotMoveRoot 表示根挂载没有父边，无法执行 MS_MOVE。
	ErrCannotMoveRoot = fmt.Errorf("root mount cannot be moved")
	// ErrCycle 表示在共享/从属组之间建立了循环（从属关系必须保持有向无环）。
	ErrCycle = fmt.Errorf("propagation group cycle")
	// ErrHostForbidden 表示传播会抵达宿主命名空间，而策略禁止宿主可见。
	ErrHostForbidden = fmt.Errorf("propagation would reach the host namespace")
	// ErrInvalidEvent 表示重放遇到未知事件类型。
	ErrInvalidEvent = fmt.Errorf("invalid replay event")
)

// Node 是挂载图中的一个挂载实例（位于某个命名空间内）。
//
// 节点带三类标记：Private 节点不属于任何组；Shared 节点属于一个共享组
// （Group 字段）；Slave 节点属于一个从属组，其主组由所属组的 master 指向。
type Node struct {
	// ID 是挂载实例的唯一标识。
	ID string
	// Namespace 是承载该挂载的挂载命名空间标识。
	Namespace string
	// ParentID 是同一命名空间内的父挂载 ID，根挂载为空串。
	ParentID string
	// Type 是传播类型。
	Type PropType
	// Group 是所属传播组 ID；Private 节点为空串，Shared/Slave 节点必填。
	Group string
}

// group 记录一个传播组。共享组与从属组共享同一结构，由 kind 区分；
// master 仅对从属组有效，指向其唯一主组（共享组或另一个从属组）。
type group struct {
	id      string
	kind    PropType
	master  string
	members map[string]struct{}
}

// Graph 是有限的挂载传播图。零值不可用，请用 NewGraph 构造。
type Graph struct {
	nodes         map[string]*Node
	groups        map[string]*group
	children      map[string]map[string]struct{}
	hostNamespace string
}

// NewGraph 创建一个空挂载图。hostNamespace 标记宿主命名空间，
// 用于宿主可见性判定；传空串表示不设宿主。
func NewGraph(hostNamespace string) *Graph {
	return &Graph{
		nodes:         make(map[string]*Node),
		groups:        make(map[string]*group),
		children:      make(map[string]map[string]struct{}),
		hostNamespace: hostNamespace,
	}
}

// AddGroup 显式建立一个传播组。kind 只能是 Shared 或 Slave。
//
// Slave 组必须通过 master 指定主组；Shared 组的 master 必须为空。
// 从属关系构成有向无环图：若 master 已经（直接或间接）从属于新组，
// 返回 ErrCycle，拒绝建边。
func (g *Graph) AddGroup(id string, kind PropType, master string) error {
	if id == "" {
		return fmt.Errorf("%w: empty group id", ErrInvalidArgument)
	}
	if _, ok := g.groups[id]; ok {
		return fmt.Errorf("%w: %s", ErrGroupExists, id)
	}
	switch kind {
	case Shared:
		if master != "" {
			return fmt.Errorf("%w: shared group %s cannot have a master", ErrInvalidPropType, id)
		}
	case Slave:
		if master == "" {
			return fmt.Errorf("%w: slave group %s requires a master group", ErrInvalidArgument, id)
		}
		m, ok := g.groups[master]
		if !ok {
			return fmt.Errorf("%w: master %s", ErrGroupNotFound, master)
		}
		if m.kind != Shared && m.kind != Slave {
			return fmt.Errorf("%w: master group %s", ErrInvalidPropType, master)
		}
		if g.groupReaches(id, master) || master == id {
			return fmt.Errorf("%w: slave group %s under %s", ErrCycle, id, master)
		}
	default:
		return fmt.Errorf("%w: %s", ErrInvalidPropType, kind)
	}
	g.groups[id] = &group{
		id:      id,
		kind:    kind,
		master:  master,
		members: make(map[string]struct{}),
	}
	return nil
}

// AddNode 在图中加入一个挂载节点，并维护父子边。
//
// Private 节点的 Group 必须为空；Shared/Slave 节点必须加入已存在且类型匹配的组。
// 非根节点的父挂载必须存在且位于同一命名空间。
func (g *Graph) AddNode(n Node) error {
	if n.ID == "" || n.Namespace == "" {
		return fmt.Errorf("%w: node id and namespace are required", ErrInvalidArgument)
	}
	if _, ok := g.nodes[n.ID]; ok {
		return fmt.Errorf("%w: %s", ErrNodeExists, n.ID)
	}
	switch n.Type {
	case Private:
		if n.Group != "" {
			return fmt.Errorf("%w: private node %s cannot join a group", ErrGroupKindMismatch, n.ID)
		}
	case Shared, Slave:
		grp, ok := g.groups[n.Group]
		if !ok {
			return fmt.Errorf("%w: %s", ErrGroupNotFound, n.Group)
		}
		if grp.kind != n.Type {
			return fmt.Errorf("%w: node %s is %s but group %s is %s",
				ErrGroupKindMismatch, n.ID, n.Type, n.Group, grp.kind)
		}
	default:
		return fmt.Errorf("%w: %s", ErrInvalidPropType, n.Type)
	}
	if n.ParentID != "" {
		parent, ok := g.nodes[n.ParentID]
		if !ok {
			return fmt.Errorf("%w: parent %s", ErrNodeNotFound, n.ParentID)
		}
		if parent.Namespace != n.Namespace {
			return fmt.Errorf("%w: node %s in %s vs parent %s in %s",
				ErrParentNamespace, n.ID, n.Namespace, parent.ID, parent.Namespace)
		}
	}
	stored := n
	g.nodes[n.ID] = &stored
	if n.ParentID != "" {
		kids := g.children[n.ParentID]
		if kids == nil {
			kids = make(map[string]struct{})
			g.children[n.ParentID] = kids
		}
		kids[n.ID] = struct{}{}
	}
	if n.Group != "" {
		g.groups[n.Group].members[n.ID] = struct{}{}
	}
	return nil
}

// RemoveNode 删除一个叶子挂载节点。仍有子挂载的节点返回 ErrHasChildren，
// 以保证父子关系不出现悬空引用。
func (g *Graph) RemoveNode(id string) error {
	node, ok := g.nodes[id]
	if !ok {
		return fmt.Errorf("%w: %s", ErrNodeNotFound, id)
	}
	if len(g.children[id]) > 0 {
		return fmt.Errorf("%w: %s", ErrHasChildren, id)
	}
	if node.ParentID != "" {
		delete(g.children[node.ParentID], id)
		if len(g.children[node.ParentID]) == 0 {
			delete(g.children, node.ParentID)
		}
	}
	if node.Group != "" {
		delete(g.groups[node.Group].members, id)
	}
	delete(g.nodes, id)
	delete(g.children, id)
	return nil
}

// SetMaster 重绑从属组的主组，共享组返回 ErrInvalidPropType。
// 新主组必须存在，且不得通过从属边回到该组自身（ErrCycle）。
// 该操作可用于显式断开传播：把从属组切到另一个主组即切断原主组的下游。
func (g *Graph) SetMaster(slaveGroup, master string) error {
	if master == "" {
		return fmt.Errorf("%w: slave group %s requires a master group", ErrInvalidArgument, slaveGroup)
	}
	grp, ok := g.groups[slaveGroup]
	if !ok {
		return fmt.Errorf("%w: %s", ErrGroupNotFound, slaveGroup)
	}
	if grp.kind != Slave {
		return fmt.Errorf("%w: group %s is %s", ErrInvalidPropType, slaveGroup, grp.kind)
	}
	m, ok := g.groups[master]
	if !ok {
		return fmt.Errorf("%w: master %s", ErrGroupNotFound, master)
	}
	if m.kind != Shared && m.kind != Slave {
		return fmt.Errorf("%w: master group %s", ErrInvalidPropType, master)
	}
	if master == slaveGroup || g.groupReaches(slaveGroup, master) {
		return fmt.Errorf("%w: slave group %s under %s", ErrCycle, slaveGroup, master)
	}
	grp.master = master
	return nil
}

// groupReaches 判断 from 组是否能沿“主组 -> 从属组”边到达 to 组。
func (g *Graph) groupReaches(from, to string) bool {
	visited := map[string]bool{from: true}
	queue := []string{from}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, grp := range g.groups {
			if grp.kind == Slave && grp.master == cur && !visited[grp.id] {
				if grp.id == to {
					return true
				}
				visited[grp.id] = true
				queue = append(queue, grp.id)
			}
		}
	}
	return false
}

// Move 模拟 MS_MOVE：把节点 id 重新挂到同一命名空间的 newParent 下。
//
// 根挂载不可移动；目标不能是节点自身或其后代；节点可带子树整体移动，
// 节点的传播类型与组成员身份保持不变。移动成功后返回从被移动节点发起的
// 传播报告，供控制面判断该更新的影响面。
func (g *Graph) Move(id, newParent string) (*Report, error) {
	node, ok := g.nodes[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNodeNotFound, id)
	}
	if node.ParentID == "" {
		return nil, fmt.Errorf("%w: %s", ErrCannotMoveRoot, id)
	}
	target, ok := g.nodes[newParent]
	if !ok {
		return nil, fmt.Errorf("%w: parent %s", ErrNodeNotFound, newParent)
	}
	if target.Namespace != node.Namespace {
		return nil, fmt.Errorf("%w: node %s in %s vs new parent %s in %s",
			ErrParentNamespace, id, node.Namespace, newParent, target.Namespace)
	}
	if id == newParent || g.isDescendant(id, newParent) {
		return nil, fmt.Errorf("%w: %s under %s", ErrMoveToDescendant, id, newParent)
	}

	delete(g.children[node.ParentID], id)
	if len(g.children[node.ParentID]) == 0 {
		delete(g.children, node.ParentID)
	}
	node.ParentID = newParent
	kids := g.children[newParent]
	if kids == nil {
		kids = make(map[string]struct{})
		g.children[newParent] = kids
	}
	kids[id] = struct{}{}

	return g.Propagate(id), nil
}

// isDescendant 判断 candidate 是否为 ancestor 的后代（不含 ancestor 自身）。
func (g *Graph) isDescendant(ancestor, candidate string) bool {
	queue := []string{ancestor}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for child := range g.children[cur] {
			if child == candidate {
				return true
			}
			queue = append(queue, child)
		}
	}
	return false
}

// Node 返回节点副本，不存在时第二个返回值为 false。
func (g *Graph) Node(id string) (Node, bool) {
	node, ok := g.nodes[id]
	if !ok {
		return Node{}, false
	}
	return *node, true
}

// GroupMaster 返回从属组的主组 ID；共享组或不存在的组返回 ("", false)。
func (g *Graph) GroupMaster(groupID string) (string, bool) {
	grp, ok := g.groups[groupID]
	if !ok || grp.kind != Slave {
		return "", false
	}
	return grp.master, true
}

// GroupMembers 返回组成员节点 ID 的有序快照。
func (g *Graph) GroupMembers(groupID string) ([]string, bool) {
	grp, ok := g.groups[groupID]
	if !ok {
		return nil, false
	}
	ids := make([]string, 0, len(grp.members))
	for id := range grp.members {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, true
}

// SubtreeIDs 返回以 id 为根的整棵子树（含 id）的有序节点 ID。
func (g *Graph) SubtreeIDs(id string) ([]string, bool) {
	if _, ok := g.nodes[id]; !ok {
		return nil, false
	}
	var out []string
	queue := []string{id}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		out = append(out, cur)
		for child := range g.children[cur] {
			queue = append(queue, child)
		}
	}
	sort.Strings(out)
	return out, true
}

// HostNamespace 返回构造时配置的宿主命名空间标识。
func (g *Graph) HostNamespace() string {
	return g.hostNamespace
}

// graphSnapshot 是 Graph 的深拷贝，用于“预演后回滚”。
type graphSnapshot struct {
	nodes    map[string]*Node
	groups   map[string]*group
	children map[string]map[string]struct{}
}

// snapshot 返回当前图状态的独立深拷贝。
func (g *Graph) snapshot() graphSnapshot {
	nodes := make(map[string]*Node, len(g.nodes))
	for id, node := range g.nodes {
		copied := *node
		nodes[id] = &copied
	}

	groups := make(map[string]*group, len(g.groups))
	for id, grp := range g.groups {
		members := make(map[string]struct{}, len(grp.members))
		for member := range grp.members {
			members[member] = struct{}{}
		}
		groups[id] = &group{
			id:      grp.id,
			kind:    grp.kind,
			master:  grp.master,
			members: members,
		}
	}

	children := make(map[string]map[string]struct{}, len(g.children))
	for parent, kids := range g.children {
		kidCopy := make(map[string]struct{}, len(kids))
		for child := range kids {
			kidCopy[child] = struct{}{}
		}
		children[parent] = kidCopy
	}

	return graphSnapshot{nodes: nodes, groups: groups, children: children}
}

// restore 用快照覆盖图状态，宿主命名空间配置保持不变。
func (g *Graph) restore(s graphSnapshot) {
	g.nodes = s.nodes
	g.groups = s.groups
	g.children = s.children
}
