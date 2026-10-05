package sim

import (
	"fmt"
	"sort"
)

// OpType 变更类型；删除也是变更。
type OpType string

const (
	OpSet OpType = "set"
	OpDel OpType = "del"
)

// Change 是槽内整数序号的键值变更日志条目。
type Change struct {
	Seq   int    `json:"seq"`
	Epoch int    `json:"epoch"`
	Op    OpType `json:"op"`
	Key   string `json:"key"`
	Val   string `json:"val,omitempty"`
	Idem  string `json:"idem,omitempty"`
}

func entrySize(c Change) int { return len(c.Key) + len(c.Val) + len(c.Idem) + 16 }

func sameContent(a, b Change) bool {
	return a.Seq == b.Seq && a.Epoch == b.Epoch && a.Op == b.Op &&
		a.Key == b.Key && a.Val == b.Val && a.Idem == b.Idem
}

// IdemRecord 幂等结果，随日志一起复制，保证重复请求跨切换仍返回原结果。
type IdemRecord struct {
	Epoch int `json:"epoch"`
	Seq   int `json:"seq"`
}

// SlotDisk 是节点上某个槽的持久化状态（崩溃后保留）。
type SlotDisk struct {
	Epoch        int                   `json:"epoch"` // 本节点作为当前主接受写所用的 epoch；0 表示不是主
	Log          []Change              `json:"log"`   // seq 从 1 开始连续
	Idem         map[string]IdemRecord `json:"idem"`
	Fenced       bool                  `json:"fenced"`
	FenceSeq     int                   `json:"fenceSeq"`
	ConfirmedSeq int                   `json:"confirmedSeq"` // 迁移目标侧：已持久化的连续前缀
}

func (s *SlotDisk) lastSeq() int { return len(s.Log) }

func (s *SlotDisk) applyChange(c Change) {
	s.Log = append(s.Log, c)
	s.ConfirmedSeq = c.Seq
	if c.Idem != "" {
		s.Idem[c.Idem] = IdemRecord{Epoch: c.Epoch, Seq: c.Seq}
	}
}

// kv 从日志重放出当前键值视图。
func (s *SlotDisk) kv() map[string]string {
	out := map[string]string{}
	for _, c := range s.Log {
		if c.Op == OpSet {
			out[c.Key] = c.Val
		} else {
			delete(out, c.Key)
		}
	}
	return out
}

// NodeDisk 节点持久化状态。
type NodeDisk struct {
	Slots map[int]*SlotDisk `json:"slots"`
}

// slotVolatile 目标侧易失状态：乱序暂存的未确认增量（崩溃即丢，由源重发）。
type slotVolatile struct {
	Pending map[int]Change
	Bytes   int
}

// Node 模拟节点：disk 持久、vol 易失。无真实网络与并发。
type Node struct {
	ID     string `json:"id"`
	Domain string `json:"domain"`
	Disk   *NodeDisk
	Vol    map[int]*slotVolatile
}

func newNode(id, domain string, M int) *Node {
	n := &Node{ID: id, Domain: domain, Disk: &NodeDisk{Slots: map[int]*SlotDisk{}}, Vol: map[int]*slotVolatile{}}
	for s := 0; s < M; s++ {
		n.Disk.Slots[s] = &SlotDisk{Idem: map[string]IdemRecord{}}
		n.Vol[s] = &slotVolatile{Pending: map[int]Change{}}
	}
	return n
}

// crash 清空易失状态（等价进程重启），持久化状态保留。
func (n *Node) crash() {
	for s, v := range n.Vol {
		v.Pending = map[int]Change{}
		v.Bytes = 0
		_ = s
	}
}

// WriteResult 逐请求结果。
type WriteResult struct {
	Status string `json:"status"` // OK / Dup / Retry / Error
	Slot   int    `json:"slot"`
	Node   string `json:"node"`
	Epoch  int    `json:"epoch,omitempty"`
	Seq    int    `json:"seq,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// applyWrite 主节点写路径：fence 检查 → epoch 检查 → 幂等 → 追加日志并持久化。
func (n *Node) applyWrite(slot, epoch int, op OpType, key, val, idem string) WriteResult {
	s := n.Disk.Slots[slot]
	if s.Fenced {
		return WriteResult{Status: "Retry", Slot: slot, Node: n.ID, Detail: "旧主已冻结(fence持久化)，拒绝新写"}
	}
	if s.Epoch == 0 || epoch != s.Epoch {
		return WriteResult{Status: "Retry", Slot: slot, Node: n.ID, Detail: fmt.Sprintf("epoch不匹配(节点epoch=%d, 请求epoch=%d)", s.Epoch, epoch)}
	}
	if rec, ok := s.Idem[idem]; ok {
		return WriteResult{Status: "Dup", Slot: slot, Node: n.ID, Epoch: rec.Epoch, Seq: rec.Seq, Detail: "幂等命中，返回已记录结果"}
	}
	c := Change{Seq: s.lastSeq() + 1, Epoch: epoch, Op: op, Key: key, Val: val, Idem: idem}
	s.Log = append(s.Log, c)
	if idem != "" {
		s.Idem[idem] = IdemRecord{Epoch: epoch, Seq: c.Seq}
	}
	return WriteResult{Status: "OK", Slot: slot, Node: n.ID, Epoch: epoch, Seq: c.Seq}
}

// Config 路由表版本。
type Config struct {
	Version int      `json:"version"`
	Primary []string `json:"primary"`
	Replica []string `json:"replica"`
	Epoch   []int    `json:"epoch"`
}

// DiffConfig 返回确实换主的槽位集合。
func DiffConfig(a, b *Config) []int {
	var out []int
	for s := range a.Primary {
		if a.Primary[s] != b.Primary[s] {
			out = append(out, s)
		}
	}
	return out
}

// Msg 在途复制消息。
type Msg struct {
	Slot int    `json:"slot"`
	Ch   Change `json:"ch"`
	To   string `json:"to"`
	Size int    `json:"size"`
}

// Event 证据事件（扁平结构，JSON 输出）。
type Event struct {
	Kind    string `json:"kind"` // req/route/phase/fence/decision/crash/recover/backpressure/conflict/dup-ign/note
	Slot    int    `json:"slot,omitempty"`
	Node    string `json:"node,omitempty"`
	Epoch   int    `json:"epoch,omitempty"`
	Seq     int    `json:"seq,omitempty"`
	Idem    string `json:"idem,omitempty"`
	Op      string `json:"op,omitempty"`
	Key     string `json:"key,omitempty"`
	Val     string `json:"val,omitempty"`
	Status  string `json:"status,omitempty"`
	Phase   string `json:"phase,omitempty"`
	Version int    `json:"version,omitempty"`
	Peer    string `json:"peer,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

// Cluster 集群模拟器：同步、确定性，无任何 goroutine/时钟。
type Cluster struct {
	M            int
	B            int // 全局未确认增量缓存上限（字节）
	Nodes        map[string]*Node
	Order        []string
	Cfg          *Config
	CfgHistory   []*Config
	Migs         map[int]*Migration // 持久化（控制器磁盘）
	Decisions    map[int]Decision   // 持久化
	Buf          []Msg              // 在途消息（易失）
	BufUsed      int
	PendingBytes int // 各目标乱序暂存总字节
	Ev           []Event
}

// Decision 持久化的路由切换决议。
type Decision struct {
	Slot       int    `json:"slot"`
	NewPrimary string `json:"newPrimary"`
	OldPrimary string `json:"oldPrimary"`
	Epoch      int    `json:"epoch"`
	FinalSeq   int    `json:"finalSeq"`
}

// NewCluster 构建集群并生成初始配置（epoch 全为 1，版本 1）。
func NewCluster(M, B int, nodes []Node) (*Cluster, error) {
	c := &Cluster{
		M: M, B: B,
		Nodes:     map[string]*Node{},
		Migs:      map[int]*Migration{},
		Decisions: map[int]Decision{},
	}
	for _, n := range nodes {
		c.Nodes[n.ID] = newNode(n.ID, n.Domain, M)
		c.Order = append(c.Order, n.ID)
	}
	sort.Strings(c.Order)
	primary, err := Assign(nodes, M)
	if err != nil {
		return nil, err
	}
	replica, err := PickReplica(nodes, M, primary)
	if err != nil {
		return nil, err
	}
	epoch := make([]int, M)
	for i := range epoch {
		epoch[i] = 1
	}
	c.Cfg = &Config{Version: 1, Primary: primary, Replica: replica, Epoch: epoch}
	c.CfgHistory = append(c.CfgHistory, c.Cfg)
	for s := 0; s < M; s++ {
		c.Nodes[primary[s]].Disk.Slots[s].Epoch = 1
	}
	c.emit(Event{Kind: "route", Version: 1, Detail: fmt.Sprintf("初始路由 primary=%v replica=%v", primary, replica)})
	return c, nil
}

func (c *Cluster) emit(e Event) { c.Ev = append(c.Ev, e) }

func (c *Cluster) configAt(v int) *Config {
	if v <= 0 || v > len(c.CfgHistory) {
		return c.Cfg
	}
	return c.CfgHistory[v-1]
}

// Request 客户端请求；CfgVer 是客户端持有的路由表版本（可为旧版）。
type Request struct {
	Op     OpType
	Key    string
	Val    string
	Idem   string
	CfgVer int
}

// Handle 处理写/删请求：按客户端持有的配置路由，旧配置请求可能被 fence 拒绝。
func (c *Cluster) Handle(r Request) WriteResult {
	cfg := c.configAt(r.CfgVer)
	slot := SlotOf(r.Key, c.M)
	primary := cfg.Primary[slot]
	res := c.Nodes[primary].applyWrite(slot, cfg.Epoch[slot], r.Op, r.Key, r.Val, r.Idem)
	c.emit(Event{Kind: "req", Slot: slot, Node: primary, Epoch: res.Epoch, Seq: res.Seq,
		Idem: r.Idem, Op: string(r.Op), Key: r.Key, Val: r.Val, Status: res.Status, Detail: res.Detail})
	return res
}

// Read 按客户端配置版本读当前主。
func (c *Cluster) Read(key string, cfgVer int) (string, bool) {
	cfg := c.configAt(cfgVer)
	slot := SlotOf(key, c.M)
	v, ok := c.Nodes[cfg.Primary[slot]].Disk.Slots[slot].kv()[key]
	return v, ok
}

// tryPush 全局缓存配额：在途 + 目标暂存不得超过 B，超出即背压且不丢记录。
func (c *Cluster) tryPush(m Msg) bool {
	if c.BufUsed+c.PendingBytes+m.Size > c.B {
		return false
	}
	c.Buf = append(c.Buf, m)
	c.BufUsed += m.Size
	return true
}

// Deliver 投递 count 条在途消息；mode: fifo / lifo(乱序) / dup(重复投递)。
func (c *Cluster) Deliver(count int, mode string) {
	for i := 0; i < count && len(c.Buf) > 0; i++ {
		var m Msg
		switch mode {
		case "lifo":
			m = c.Buf[len(c.Buf)-1]
			c.Buf = c.Buf[:len(c.Buf)-1]
		case "dup":
			m = c.Buf[0]
			c.receive(m) // 第一次投递
			c.Buf = c.Buf[1:]
		default: // fifo
			m = c.Buf[0]
			c.Buf = c.Buf[1:]
		}
		c.BufUsed -= m.Size
		c.receive(m)
	}
}

// receive 目标侧接收：只确认连续前缀；乱序暂存；重复校验内容一致；同序号不同内容报错。
func (c *Cluster) receive(m Msg) {
	t := c.Nodes[m.To]
	s := t.Disk.Slots[m.Slot]
	tv := t.Vol[m.Slot]
	ch := m.Ch
	if ch.Seq <= s.ConfirmedSeq {
		if !sameContent(s.Log[ch.Seq-1], ch) {
			c.emit(Event{Kind: "conflict", Slot: m.Slot, Node: t.ID, Seq: ch.Seq,
				Detail: fmt.Sprintf("同序号不同内容: 已持久化 %+v, 收到 %+v", s.Log[ch.Seq-1], ch)})
		} else {
			c.emit(Event{Kind: "dup-ign", Slot: m.Slot, Node: t.ID, Seq: ch.Seq, Detail: "重复增量，内容一致，忽略"})
		}
		return
	}
	if ch.Seq == s.ConfirmedSeq+1 {
		s.applyChange(ch)
		for { // 连续前缀推进：冲刷乱序暂存
			nx, ok := tv.Pending[s.ConfirmedSeq+1]
			if !ok {
				break
			}
			s.applyChange(nx)
			delete(tv.Pending, nx.Seq)
			tv.Bytes -= entrySize(nx)
			c.PendingBytes -= entrySize(nx)
		}
		c.maybeAdvance(m.Slot)
		return
	}
	if old, ok := tv.Pending[ch.Seq]; ok {
		if !sameContent(old, ch) {
			c.emit(Event{Kind: "conflict", Slot: m.Slot, Node: t.ID, Seq: ch.Seq,
				Detail: fmt.Sprintf("暂存区同序号不同内容: %+v vs %+v", old, ch)})
		}
		return
	}
	tv.Pending[ch.Seq] = ch
	tv.Bytes += entrySize(ch)
	c.PendingBytes += entrySize(ch)
}

// InjectConflict 故障注入：伪造一条与在途首条消息同序号不同内容的消息。
func (c *Cluster) InjectConflict() bool {
	if len(c.Buf) == 0 {
		return false
	}
	m := c.Buf[0]
	m.Ch.Val = m.Ch.Val + "!corrupt"
	if !c.tryPush(m) {
		c.Buf = append(c.Buf, m) // 注入路径不计配额，保证故障可复现
		c.BufUsed += m.Size
	}
	return true
}

// CrashAll 模拟全部节点与网络重启：易失状态（在途消息、目标暂存）丢失，持久化状态保留。
func (c *Cluster) CrashAll() {
	for _, n := range c.Nodes {
		n.crash()
	}
	c.Buf = nil
	c.BufUsed = 0
	c.PendingBytes = 0
	c.emit(Event{Kind: "crash", Detail: "全部节点重启，在途消息与乱序暂存丢失"})
}

// Recover 崩溃后恢复：复制进度、fence、切换决议、幂等结果全部来自持久化状态；
// 未完成的迁移从目标已确认进度继续（源重发未确认部分，不丢记录）。
func (c *Cluster) Recover() {
	for _, mig := range c.Migs {
		if mig.Phase != "Done" {
			confirmed := c.Nodes[mig.Target].Disk.Slots[mig.Slot].ConfirmedSeq
			if mig.SentUpTo > confirmed {
				mig.SentUpTo = confirmed
			}
		}
	}
	c.emit(Event{Kind: "recover", Detail: "从持久化状态恢复：日志/幂等/fence/决议/已确认进度"})
}

// SlotDump 单槽最终证据：当前主、epoch、KV 视图与有序变更日志。
type SlotDump struct {
	Slot    int               `json:"slot"`
	Primary string            `json:"primary"`
	Epoch   int               `json:"epoch"`
	KV      map[string]string `json:"kv"`
	Log     []Change          `json:"log"`
}

// Dump 输出当前路由、各槽阶段与有序变更证据。
type Dump struct {
	Config Config      `json:"config"`
	Migs   []Migration `json:"migs"`
	Slots  []SlotDump  `json:"slots"`
}

func (c *Cluster) Dump() Dump {
	d := Dump{Config: *c.Cfg}
	for s := 0; s < c.M; s++ {
		p := c.Cfg.Primary[s]
		sd := c.Nodes[p].Disk.Slots[s]
		d.Slots = append(d.Slots, SlotDump{
			Slot: s, Primary: p, Epoch: c.Cfg.Epoch[s], KV: sd.kv(),
			Log: append([]Change(nil), sd.Log...),
		})
	}
	for _, m := range c.Migs {
		d.Migs = append(d.Migs, *m)
	}
	sort.Slice(d.Migs, func(i, j int) bool { return d.Migs[i].Slot < d.Migs[j].Slot })
	return d
}
