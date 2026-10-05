package main

import (
	"fmt"
	"sort"
	"strings"
)

const (
	clusterSize = 3
	ctrlID      = 99 // controller 在网络中的虚拟节点 id
)

// ---------- 日志条目 ----------

type Entry struct {
	Term      int
	Key       string
	Value     string
	ClientID  string
	RequestID int
}

func (e Entry) String() string {
	return fmt.Sprintf("[T%d %s=%s %s#%d]", e.Term, e.Key, e.Value, e.ClientID, e.RequestID)
}

// ---------- 复制消息 ----------

type ReplMsg struct {
	From      int
	To        int
	Term      int
	PrevIndex int // Entries 之前已有的条目数（前缀长度）
	Entries   []Entry
	Commit    int // 发送方的提交水位
}

func (m ReplMsg) String() string {
	parts := make([]string, len(m.Entries))
	for i, e := range m.Entries {
		parts[i] = e.String()
	}
	return fmt.Sprintf("repl(N%d->N%d T%d prev=%d entries=%s commit=%d)",
		m.From, m.To, m.Term, m.PrevIndex, strings.Join(parts, ""), m.Commit)
}

// ---------- 写请求 / 响应 ----------

type WriteReq struct {
	Key       string
	Value     string
	ClientID  string
	RequestID int
}

type Response struct {
	OK       bool
	Redirect int // -1 表示无重定向信息
	Reason   string
}

func (r Response) String() string {
	if r.OK {
		return "OK: " + r.Reason
	}
	if r.Redirect >= 0 {
		return fmt.Sprintf("REJECT -> redirect to N%d (%s)", r.Redirect, r.Reason)
	}
	return "FAIL: " + r.Reason
}

// ---------- 节点 ----------

type Node struct {
	ID        int
	Role      string // "primary" / "follower"
	Term      int
	Log       []Entry
	CommitIdx int // 已提交条目数（前缀长度）
	applied   int
	Store     map[string]string
	LastReq   map[string]int    // clientID -> 已处理的最大 requestID（去重表）
	LastReply map[string]string // clientID -> 最近一次响应
	Down      bool
}

func NewNode(id int) *Node {
	return &Node{
		ID:        id,
		Role:      "follower",
		Store:     map[string]string{},
		LastReq:   map[string]int{},
		LastReply: map[string]string{},
	}
}

func (n *Node) apply() {
	for n.applied < n.CommitIdx && n.applied < len(n.Log) {
		e := n.Log[n.applied]
		n.Store[e.Key] = e.Value
		// 去重状态随已提交日志一起重建（相当于随复制传播到 follower）
		if e.ClientID != "" && e.RequestID > n.LastReq[e.ClientID] {
			n.LastReq[e.ClientID] = e.RequestID
			n.LastReply[e.ClientID] = fmt.Sprintf("committed %s=%s at idx=%d term=T%d",
				e.Key, e.Value, n.applied, e.Term)
		}
		n.applied++
	}
}

func (n *Node) ObserveTerm(t int) {
	if t > n.Term {
		n.Term = t
		n.Role = "follower"
	}
}

// HandleWrite 处理客户端写请求。
// 核心不变量：只有当前任期的授权主才能提交。
func (n *Node) HandleWrite(req WriteReq, ctrl *Controller, netw *Network, nodes map[int]*Node) Response {
	if n.Down {
		return Response{Redirect: -1, Reason: "connection refused (node down)"}
	}
	ctrlReachable := netw.reachable(n.ID, ctrlID)
	authorized := ctrlReachable && ctrl.Primary == n.ID && ctrl.Term == n.Term

	// 明确知道自己不是授权主 -> 重定向；分区中的旧主（controller 不可达）不走到这里
	if n.Role != "primary" || (ctrlReachable && !authorized) {
		return Response{Redirect: ctrl.Primary,
			Reason: fmt.Sprintf("not the authorized primary (current: N%d @ T%d)", ctrl.Primary, ctrl.Term)}
	}

	// 幂等去重：同一 (clientID, requestID) 直接返回缓存响应，不重复追加日志
	if last, seen := n.LastReq[req.ClientID]; seen && req.RequestID <= last {
		return Response{OK: true, Reason: "duplicate suppressed -> " + n.LastReply[req.ClientID]}
	}

	e := Entry{Term: n.Term, Key: req.Key, Value: req.Value, ClientID: req.ClientID, RequestID: req.RequestID}
	n.Log = append(n.Log, e)
	idx := len(n.Log) - 1

	acks := 1 // 自己
	for i := 0; i < clusterSize; i++ {
		nd := nodes[i]
		if nd.ID == n.ID {
			continue
		}
		msg := ReplMsg{From: n.ID, To: nd.ID, Term: n.Term, PrevIndex: idx, Entries: []Entry{e}, Commit: n.CommitIdx}
		if netw.Send(msg, nd) {
			acks++
			continue
		}
		// 增量失败则退化为全量同步一次（修补缺失前缀）
		full := ReplMsg{From: n.ID, To: nd.ID, Term: n.Term, PrevIndex: 0,
			Entries: append([]Entry(nil), n.Log...), Commit: n.CommitIdx}
		if netw.Send(full, nd) {
			acks++
		}
	}

	quorum := acks*2 > clusterSize
	if quorum && authorized {
		n.CommitIdx = len(n.Log)
		n.apply()
		reply := n.LastReply[req.ClientID]
		// 广播提交水位
		for i := 0; i < clusterSize; i++ {
			nd := nodes[i]
			if nd.ID == n.ID {
				continue
			}
			netw.Send(ReplMsg{From: n.ID, To: nd.ID, Term: n.Term, PrevIndex: len(n.Log), Commit: n.CommitIdx}, nd)
		}
		return Response{OK: true, Reason: reply}
	}

	var why []string
	if !quorum {
		why = append(why, fmt.Sprintf("no quorum (%d/%d acks)", acks, clusterSize))
	}
	if !authorized {
		why = append(why, "cannot confirm current-term authorization")
	}
	return Response{Redirect: -1,
		Reason: "NOT committed (" + strings.Join(why, "; ") + "); entry stays uncommitted"}
}

// ReceiveReplication 处理复制消息。
// 核心不变量：过期任期的消息直接拒绝；已提交前缀永不替换。
func (n *Node) ReceiveReplication(m ReplMsg) bool {
	if n.Down {
		return false
	}
	if m.Term < n.Term {
		fmt.Printf("     N%d REJECTS stale %s (local term T%d)\n", n.ID, m, n.Term)
		return false
	}
	n.Role = "follower"
	n.Term = m.Term
	if m.PrevIndex > len(n.Log) {
		return false // 缺前缀，等待全量同步
	}
	idx := m.PrevIndex
	for _, e := range m.Entries {
		if idx < len(n.Log) {
			if n.Log[idx] != e {
				if idx < n.CommitIdx {
					fmt.Printf("     N%d REFUSES to overwrite committed entry at idx=%d\n", n.ID, idx)
					return false
				}
				fmt.Printf("     N%d truncates uncommitted suffix from idx=%d\n", n.ID, idx)
				n.Log = n.Log[:idx]
				n.Log = append(n.Log, e)
			}
		} else {
			n.Log = append(n.Log, e)
		}
		idx++
	}
	if m.Commit > n.CommitIdx {
		n.CommitIdx = min(m.Commit, len(n.Log))
		n.apply()
	}
	return true
}

// SyncFollower 主对指定 follower 做全量同步（用于分区恢复/崩溃重连）。
func SyncFollower(p, f *Node, netw *Network) bool {
	msg := ReplMsg{From: p.ID, To: f.ID, Term: p.Term, PrevIndex: 0,
		Entries: append([]Entry(nil), p.Log...), Commit: p.CommitIdx}
	return netw.Send(msg, f)
}

// ---------- 网络（支持分区 / 延迟） ----------

type Network struct {
	blocked map[[2]int]bool
	slow    map[[2]int]bool // 单向慢链路：消息进入延迟队列而非丢弃
	delayed []ReplMsg
}

func NewNetwork() *Network {
	return &Network{blocked: map[[2]int]bool{}, slow: map[[2]int]bool{}}
}

func (netw *Network) Partition(a, b int) {
	netw.blocked[[2]int{a, b}] = true
	netw.blocked[[2]int{b, a}] = true
}

func (netw *Network) Heal(a, b int) {
	delete(netw.blocked, [2]int{a, b})
	delete(netw.blocked, [2]int{b, a})
}

func (netw *Network) Slow(from, to int) { netw.slow[[2]int{from, to}] = true }

func (netw *Network) reachable(a, b int) bool { return !netw.blocked[[2]int{a, b}] }

// Send 立即投递；被分区阻断或对端宕机返回 false；慢链路进入延迟队列。
func (netw *Network) Send(m ReplMsg, to *Node) bool {
	if !netw.reachable(m.From, to.ID) || to.Down {
		return false
	}
	if netw.slow[[2]int{m.From, to.ID}] {
		netw.delayed = append(netw.delayed, m)
		return false
	}
	return to.ReceiveReplication(m)
}

// FlushDelayed 投递所有滞留消息（模拟延迟复制最终到达）。
func (netw *Network) FlushDelayed(nodes map[int]*Node) {
	msgs := netw.delayed
	netw.delayed = nil
	if len(msgs) == 0 {
		fmt.Println("  (no delayed messages)")
		return
	}
	for _, m := range msgs {
		fmt.Printf("  delivering delayed %s\n", m)
		to := nodes[m.To]
		if netw.reachable(m.From, m.To) && !to.Down {
			to.ReceiveReplication(m)
		} else {
			fmt.Println("     dropped (peer unreachable or down)")
		}
	}
}

// ---------- Controller（任期授权中心） ----------

type Controller struct {
	Term    int
	Primary int
}

func (c *Controller) Elect(id int, nodes map[int]*Node, netw *Network) {
	c.Term++
	c.Primary = id
	p := nodes[id]
	p.Role = "primary"
	p.Term = c.Term
	fmt.Printf("  controller: N%d elected primary for T%d\n", id, c.Term)
	for i := 0; i < clusterSize; i++ {
		nd := nodes[i]
		if nd.ID == id || nd.Down {
			continue
		}
		if netw.reachable(id, nd.ID) {
			nd.ObserveTerm(c.Term)
		}
	}
}

// ---------- 客户端 ----------

type Client struct {
	ID     string
	Cached int // 缓存的主地址（可能是旧主）
}

// ---------- 状态打印 ----------

func (n *Node) logStr() string {
	if len(n.Log) == 0 {
		return "(empty)"
	}
	parts := make([]string, len(n.Log))
	for i, e := range n.Log {
		mark := "x" // 未提交
		if i < n.CommitIdx {
			mark = "V" // 已提交
		}
		parts[i] = mark + e.String()
	}
	return strings.Join(parts, " ")
}

func (n *Node) storeStr() string {
	if len(n.Store) == 0 {
		return "{}"
	}
	keys := make([]string, 0, len(n.Store))
	for k := range n.Store {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + "=" + n.Store[k]
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func dump(nodes map[int]*Node, ctrl *Controller) {
	fmt.Printf("  -- controller: term=T%d primary=N%d\n", ctrl.Term, ctrl.Primary)
	for i := 0; i < clusterSize; i++ {
		n := nodes[i]
		state := n.Role
		if n.Down {
			state = "DOWN"
		}
		fmt.Printf("  N%d  %-8s term=T%d commit=%d\n      log:   %s\n      store: %s\n",
			n.ID, state, n.Term, n.CommitIdx, n.logStr(), n.storeStr())
	}
	if p, ok := nodes[ctrl.Primary]; ok && !p.Down {
		fmt.Printf("  => client-visible (committed store @N%d): %s\n", p.ID, p.storeStr())
	}
}

// Delay 把一条消息直接放入延迟队列（模拟滞留在网络缓冲区中的复制消息）。
func (netw *Network) Delay(m ReplMsg) { netw.delayed = append(netw.delayed, m) }
