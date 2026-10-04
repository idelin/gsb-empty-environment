// 主从写入模拟器：基于任期的写入日志。
//
// 核心规则：
//  1. 提交仅接受当前任期的授权者（TermAuthority 记录的 current primary）。
//     旧主（客户端缓存了旧地址）可以收下写入，但提交会被授权方拒绝。
//  2. 复制以 (term, log, commitIdx) 快照进行；延迟到达的复制消息
//     不得替换接收方已提交的前缀，只能截断未提交的后缀。
//  3. 客户端请求带 RequestID，重复请求幂等。
package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// ---------- 数据模型 ----------

type Entry struct {
	Term  int
	Key   string
	Value string
	ReqID string
}

func (e Entry) String() string {
	return fmt.Sprintf("%s=%s@t%d(%s)", e.Key, e.Value, e.Term, e.ReqID)
}

type Node struct {
	ID        string
	Term      int
	Log       []Entry
	CommitIdx int // 已提交前缀长度
	Store     map[string]string
	Seen      map[string]string // ReqID -> 已应用值（幂等去重）
}

func NewNode(id string) *Node {
	return &Node{ID: id, Store: map[string]string{}, Seen: map[string]string{}}
}

func (n *Node) logString() string {
	if len(n.Log) == 0 {
		return "[]"
	}
	parts := make([]string, len(n.Log))
	for i, e := range n.Log {
		mark := " "
		if i < n.CommitIdx {
			mark = "*"
		}
		parts[i] = mark + e.String()
	}
	return "[" + strings.Join(parts, " ") + "]"
}

func (n *Node) storeString() string {
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
	return "{" + strings.Join(parts, " ") + "}"
}

// apply 应用 [oldCommit, newCommit) 区间的日志到状态机，按 ReqID 幂等。
func (n *Node) apply(newCommit int) {
	for i := n.CommitIdx; i < newCommit; i++ {
		e := n.Log[i]
		if _, dup := n.Seen[e.ReqID]; !dup {
			n.Seen[e.ReqID] = e.Value
			n.Store[e.Key] = e.Value
		}
	}
	n.CommitIdx = newCommit
}

// ---------- 任期授权方（配置中心） ----------

type Authority struct {
	Term    int
	Primary string
}

// AuthorizeCommit 提交仅接受当前任期的授权者。
func (a *Authority) AuthorizeCommit(n *Node) bool {
	return n.Term == a.Term && n.ID == a.Primary
}

func (a *Authority) Elect(primary string) {
	a.Term++
	a.Primary = primary
}

// ---------- 集群 / 网络模拟 ----------

type Message struct {
	From      string
	To        string
	Term      int
	Log       []Entry
	CommitIdx int
	DeliverAt int
}

type Cluster struct {
	Auth     *Authority
	Nodes    map[string]*Node
	Blocked  map[[2]string]bool // 分区：单向断链
	Queue    []Message          // 延迟消息
	Clock    int
	StepNote string
}

func NewCluster(ids ...string) *Cluster {
	c := &Cluster{
		Auth:    &Authority{Term: 1, Primary: ids[0]},
		Nodes:   map[string]*Node{},
		Blocked: map[[2]string]bool{},
	}
	for _, id := range ids {
		n := NewNode(id)
		n.Term = 1
		c.Nodes[id] = n
	}
	return c
}

func (c *Cluster) Partition(a, b string) {
	c.Blocked[[2]string{a, b}] = true
	c.Blocked[[2]string{b, a}] = true
}

func (c *Cluster) Heal(a, b string) {
	delete(c.Blocked, [2]string{a, b})
	delete(c.Blocked, [2]string{b, a})
}

func (c *Cluster) reachable(a, b string) bool { return !c.Blocked[[2]string{a, b}] }

// replicate 主向所有可达从节点发送日志快照；delay>0 的消息进入延迟队列。
func (c *Cluster) replicate(from *Node, delayFor map[string]int) {
	for _, n := range c.Nodes {
		if n.ID == from.ID || !c.reachable(from.ID, n.ID) {
			continue
		}
		msg := Message{
			From: from.ID, To: n.ID, Term: from.Term,
			Log: append([]Entry(nil), from.Log...), CommitIdx: from.CommitIdx,
		}
		if d, ok := delayFor[n.ID]; ok && d > 0 {
			msg.DeliverAt = c.Clock + d
			c.Queue = append(c.Queue, msg)
			fmt.Printf("    ~ 复制 %s->%s 延迟 %d 拍入队\n", from.ID, n.ID, d)
			continue
		}
		c.deliver(msg)
	}
}

func (c *Cluster) deliver(m Message) {
	to := c.Nodes[m.To]
	if !c.reachable(m.From, m.To) {
		fmt.Printf("    x 复制 %s->%s 被分区丢弃\n", m.From, m.To)
		return
	}
	if m.Term < to.Term {
		fmt.Printf("    x %s 拒绝来自 %s 的过期任期复制 (msgTerm=%d < nodeTerm=%d)\n",
			to.ID, m.From, m.Term, to.Term)
		return
	}
	to.Term = m.Term
	// 合并：已提交前缀不可替换；冲突时仅截断未提交后缀。
	for i, e := range m.Log {
		if i < to.CommitIdx {
			if i >= len(to.Log) || to.Log[i] != e {
				fmt.Printf("    x %s 拒绝复制：企图覆盖已提交前缀 idx=%d\n", to.ID, i)
				return
			}
			continue
		}
		if i < len(to.Log) {
			if to.Log[i] != e {
				fmt.Printf("    ! %s 截断未提交后缀 [%d..] %v\n", to.ID, i, to.Log[i:])
				to.Log = to.Log[:i]
				to.Log = append(to.Log, m.Log[i:]...)
			}
		} else {
			to.Log = append(to.Log, m.Log[i:]...)
		}
		break
	}
	if len(m.Log) < len(to.Log) && len(m.Log) >= to.CommitIdx {
		fmt.Printf("    ! %s 截断未提交后缀 [%d..] %v\n", to.ID, len(m.Log), to.Log[len(m.Log):])
		to.Log = to.Log[:len(m.Log)]
	}
	if m.CommitIdx > to.CommitIdx {
		to.apply(m.CommitIdx)
	}
	fmt.Printf("    -> %s 接受 %s 的复制 (term=%d)\n", to.ID, m.From, m.Term)
}

// Tick 推进逻辑时钟，投递到期的延迟消息。
func (c *Cluster) Tick(note string) {
	c.Clock++
	fmt.Printf("  [tick %d] %s\n", c.Clock, note)
	for i := 0; i < len(c.Queue); {
		if c.Queue[i].DeliverAt <= c.Clock {
			m := c.Queue[i]
			c.Queue = append(c.Queue[:i], c.Queue[i+1:]...)
			fmt.Printf("    ~ 延迟消息到期 %s->%s (term=%d)\n", m.From, m.To, m.Term)
			c.deliver(m)
		} else {
			i++
		}
	}
}

// Elect 授权方选举新主；新主进入新任期。
func (c *Cluster) Elect(primary string) {
	c.Auth.Elect(primary)
	c.Nodes[primary].Term = c.Auth.Term
	fmt.Printf("  ** 授权方选举: primary=%s term=%d\n", primary, c.Auth.Term)
}

// ---------- 客户端 ----------

type Client struct {
	CachedPrimary string
	ReqCounter    int
}

// Write 客户端写入：发往缓存的主地址；失败则向授权方刷新地址并以同一 ReqID 重试。
func (cl *Client) Write(c *Cluster, key, value string) {
	cl.ReqCounter++
	reqID := fmt.Sprintf("r%d", cl.ReqCounter)
	cl.writeWith(c, reqID, key, value)
}

// Retry 模拟重复请求（网络重试/用户重发），复用同一 ReqID。
func (cl *Client) Retry(c *Cluster, reqID string, key, value string) {
	cl.writeWith(c, reqID, key, value)
}

func (cl *Client) writeWith(c *Cluster, reqID, key, value string) {
	for attempt := 1; attempt <= 3; attempt++ {
		p := c.Nodes[cl.CachedPrimary]
		fmt.Printf("  客户端 -> %s 写入 %s=%s (req=%s, 第%d次)\n", p.ID, key, value, reqID, attempt)
		if p.Term < c.Auth.Term || c.Auth.Primary != p.ID {
			fmt.Printf("    x %s 不是当前任期授权者 (nodeTerm=%d, authTerm=%d, authPrimary=%s)，写入被拒\n",
				p.ID, p.Term, c.Auth.Term, c.Auth.Primary)
			cl.CachedPrimary = c.Auth.Primary
			fmt.Printf("    客户端刷新缓存主地址 -> %s\n", cl.CachedPrimary)
			continue
		}
		if v, dup := p.Seen[reqID]; dup {
			fmt.Printf("    = %s 命中幂等去重 req=%s，直接返回已应用值 %s\n", p.ID, reqID, v)
			return
		}
		p.Log = append(p.Log, Entry{Term: p.Term, Key: key, Value: value, ReqID: reqID})
		fmt.Printf("    + %s 追加日志 %s（未提交）\n", p.ID, p.Log[len(p.Log)-1])
		// 提交前向授权方确认：仅当前任期的授权者可提交。
		if !c.Auth.AuthorizeCommit(p) {
			fmt.Printf("    x %s 提交被授权方拒绝（非当前任期授权者），条目保持未提交\n", p.ID)
			cl.CachedPrimary = c.Auth.Primary
			fmt.Printf("    客户端刷新缓存主地址 -> %s\n", cl.CachedPrimary)
			continue
		}
		p.apply(len(p.Log))
		fmt.Printf("    * %s 提交成功 (term=%d)，commitIdx=%d\n", p.ID, p.Term, p.CommitIdx)
		c.replicate(p, nil)
		return
	}
	fmt.Println("    ! 写入失败：重试次数耗尽")
}

// ---------- 状态打印 ----------

func (c *Cluster) Dump(step string) {
	fmt.Printf("\n== 步骤: %s ==\n", step)
	fmt.Printf("  授权方: term=%d primary=%s\n", c.Auth.Term, c.Auth.Primary)
	ids := make([]string, 0, len(c.Nodes))
	for id := range c.Nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		n := c.Nodes[id]
		fmt.Printf("  节点 %-3s term=%d commit=%d log=%-40s store=%s\n",
			n.ID, n.Term, n.CommitIdx, n.logString(), n.storeString())
	}
	if len(c.Queue) > 0 {
		for _, m := range c.Queue {
			fmt.Printf("  在途延迟消息: %s->%s term=%d 于 tick=%d 投递\n", m.From, m.To, m.Term, m.DeliverAt)
		}
	}
	fmt.Println()
}

// ---------- 场景脚本 ----------

func step(c *Cluster, note string) { c.Dump(note) }

// 场景 1：分区 —— 客户端缓存旧主地址，切换后旧主仍收到写入。
func scenarioPartition() {
	fmt.Println("########## 场景 1：分区与旧主写入 ##########")
	c := NewCluster("A", "B", "C")
	cl := &Client{CachedPrimary: "A"}

	step(c, "初始：A 为 term=1 授权主")
	cl.Write(c, "x", "1")
	step(c, "客户端写入 x=1，A 提交并复制到 B、C")

	c.Partition("A", "B")
	c.Partition("A", "C")
	step(c, "网络分区：A 与 {B,C} 断开")

	c.Elect("B")
	c.replicate(c.Nodes["B"], nil)
	step(c, "授权方选 B 为 term=2 新主，B 向 C 复制")

	cl.Write(c, "y", "2") // 客户端仍缓存旧主 A
	step(c, "客户端用缓存地址写旧主 A：被拒后刷新缓存，改投 B 成功")

	cl.Write(c, "z", "3")
	step(c, "客户端继续写新主 B")

	fmt.Println(">> 验证：旧主 A 的 y=2 从未提交；已提交前缀 x=1 在 B、C 上完好。")
}

// 场景 2：主恢复 —— 旧主重新加入，截断未提交后缀，保留已提交前缀。
func scenarioRecovery() {
	fmt.Println("########## 场景 2：旧主恢复 ##########")
	c := NewCluster("A", "B", "C")
	cl := &Client{CachedPrimary: "A"}

	cl.Write(c, "x", "1")
	step(c, "初始写入 x=1 已提交并复制")

	c.Partition("A", "B")
	c.Partition("A", "C")
	c.Elect("B")
	c.replicate(c.Nodes["B"], nil)
	cl.Write(c, "y", "2")
	step(c, "分区期间：B 为 term=2 主并提交 y=2；A 落后")

	// 旧主 A 在分区期间收到客户端写入，追加但未提交。
	a := c.Nodes["A"]
	a.Log = append(a.Log, Entry{Term: 1, Key: "w", Value: "9", ReqID: "stale-1"})
	fmt.Println("  旧主 A 在分区中追加未提交条目 w=9@t1")
	step(c, "A 持有未提交的陈旧条目 w=9@t1")

	c.Heal("A", "B")
	c.Heal("A", "C")
	step(c, "分区恢复")

	c.replicate(c.Nodes["B"], nil)
	step(c, "新主 B 向 A 复制：A 截断未提交的 w=9，保留已提交前缀 x=1")

	fmt.Println(">> 验证：A 的已提交前缀 x=1 未被替换；未提交的陈旧条目被截断。")
}

// 场景 3：延迟复制不得覆盖已提交前缀 + 重复请求幂等。
func scenarioDelayAndDup() {
	fmt.Println("########## 场景 3：延迟复制与重复请求 ##########")
	c := NewCluster("A", "B", "C")
	cl := &Client{CachedPrimary: "A"}

	cl.Write(c, "x", "1")
	step(c, "x=1 提交并复制到 B、C")

	// A 再写一条，但发往 C 的复制被延迟。
	a := c.Nodes["A"]
	a.Log = append(a.Log, Entry{Term: 1, Key: "y", Value: "2", ReqID: "r2"})
	a.apply(len(a.Log))
	fmt.Println("  A 提交 y=2，发往 C 的复制延迟 3 拍")
	c.replicate(a, map[string]int{"C": 3})
	step(c, "B 已同步 y=2；C 的复制在途（延迟）")

	// 延迟消息到达前，C 不应被任何旧快照回滚已提交前缀。
	c.Tick("无事件")
	c.Tick("无事件")
	c.Tick("延迟复制到期")
	step(c, "延迟复制到达 C：仅追加缺失后缀，已提交前缀 x=1 不动")

	// 构造一条来自旧任期的延迟消息，企图用 x=999 覆盖 C 已提交的 x=1。
	stale := Message{
		From: "A", To: "C", Term: 1,
		Log:       []Entry{{Term: 1, Key: "x", Value: "999", ReqID: "evil"}},
		CommitIdx: 0, DeliverAt: c.Clock + 1,
	}
	c.Queue = append(c.Queue, stale)
	fmt.Println("  一条旧任期延迟消息入队：企图把 idx=0 覆盖为 x=999")
	c.Tick("陈旧延迟消息到期")
	step(c, "C 拒绝覆盖已提交前缀：x=1 保持不动")

	// 重复请求：客户端重发 r1（x=1 的原始请求）。
	cl.Retry(c, "r1", "x", "1")
	step(c, "客户端重发 req=r1：命中幂等去重，不重复应用")

	// 同 ReqID 不同值也应被去重（以首次为准）。
	cl.Retry(c, "r1", "x", "999")
	step(c, "同 req=r1 携带不同值重发：仍返回首次结果，store 不变")

	fmt.Println(">> 验证：延迟复制未替换已提交前缀；重复请求只应用一次。")
}

func main() {
	scenario := "all"
	if len(os.Args) > 1 {
		scenario = os.Args[1]
	}
	switch scenario {
	case "partition":
		scenarioPartition()
	case "recovery":
		scenarioRecovery()
	case "dup":
		scenarioDelayAndDup()
	default:
		scenarioPartition()
		fmt.Println()
		scenarioRecovery()
		fmt.Println()
		scenarioDelayAndDup()
	}
}
