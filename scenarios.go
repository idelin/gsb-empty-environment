package main

import "fmt"

var stepNum int

func step(format string, args ...any) {
	stepNum++
	fmt.Printf("\n-- Step %d: %s --\n", stepNum, fmt.Sprintf(format, args...))
}

func newCluster() (map[int]*Node, *Network, *Controller) {
	nodes := map[int]*Node{0: NewNode(0), 1: NewNode(1), 2: NewNode(2)}
	netw := NewNetwork()
	ctrl := &Controller{Primary: -1}
	ctrl.Elect(0, nodes, netw)
	return nodes, netw, ctrl
}

func write(nodes map[int]*Node, netw *Network, ctrl *Controller, cl *Client, key, value string, reqID int) Response {
	target := nodes[cl.Cached]
	fmt.Printf("  >> %s -> N%d  WRITE %s=%s  (%s req#%d)\n", cl.ID, target.ID, key, value, cl.ID, reqID)
	resp := target.HandleWrite(WriteReq{Key: key, Value: value, ClientID: cl.ID, RequestID: reqID}, ctrl, netw, nodes)
	fmt.Printf("  << %s\n", resp)
	return resp
}

// ================= 场景 1：分区 —— 旧主地址被客户端缓存 =================

func scenarioPartition() {
	stepNum = 0
	fmt.Println("\n================ Scenario 1: 分区后旧主仍收到写入 ================")
	nodes, netw, ctrl := newCluster()
	c1 := &Client{ID: "c1", Cached: 0}

	step("初始：N0 是 T1 主；c1 写 A=1 (req#1)，多数派提交")
	write(nodes, netw, ctrl, c1, "A", "1", 1)
	dump(nodes, ctrl)

	step("网络分区：N0 与 {N1, N2, controller} 隔离；controller 选 N1 为 T2 主")
	netw.Partition(0, 1)
	netw.Partition(0, 2)
	netw.Partition(0, ctrlID)
	ctrl.Elect(1, nodes, netw)
	fmt.Println("  注意：N0 收不到新任期，仍认为自己是 T1 主")
	dump(nodes, ctrl)

	step("c1 缓存了旧主地址，仍把 X=9 (req#2) 发给 N0")
	write(nodes, netw, ctrl, c1, "X", "9", 2)
	fmt.Println("  N0 把写追加到本地日志，但拿不到多数派、也无法确认任期授权 -> 无法提交")
	netw.Delay(ReplMsg{From: 0, To: 2, Term: 1, PrevIndex: 1,
		Entries: []Entry{{Term: 1, Key: "X", Value: "9", ClientID: "c1", RequestID: 2}}, Commit: 1})
	fmt.Println("  (N0 发出的一条 T1 复制消息滞留在网络缓冲区中，稍后投递)")
	dump(nodes, ctrl)

	step("c1 向 controller 询问新主，改发 N1，重试 req#2 (X=9)")
	c1.Cached = ctrl.Primary
	write(nodes, netw, ctrl, c1, "X", "9", 2)
	dump(nodes, ctrl)

	step("分区恢复：N1 全量同步 N0")
	netw.Heal(0, 1)
	netw.Heal(0, 2)
	netw.Heal(0, ctrlID)
	SyncFollower(nodes[1], nodes[0], netw)
	fmt.Println("  N0 观察到 T2 -> 降为 follower，截断未提交的 X=9(T1)，采纳已提交日志")
	dump(nodes, ctrl)

	step("滞留的旧主 T1 复制消息到达 N2 -> 因任期过期被拒绝，已提交前缀不变")
	netw.FlushDelayed(nodes)
	dump(nodes, ctrl)
}

// ================= 场景 2：主恢复 —— 崩溃主带未提交数据重新加入 =================

func scenarioRecovery() {
	stepNum = 0
	fmt.Println("\n================ Scenario 2: 主崩溃恢复与延迟复制 ================")
	nodes, netw, ctrl := newCluster()
	c1 := &Client{ID: "c1", Cached: 0}

	step("c1 写 A=1 (req#1)；N0->N2 链路慢，N0+N1 构成多数派提交")
	netw.Slow(0, 2)
	write(nodes, netw, ctrl, c1, "A", "1", 1)
	dump(nodes, ctrl)

	step("c1 写 Y=7 (req#2)；N0->N1 也变慢，N0 未提交即宕机")
	netw.Slow(0, 1)
	write(nodes, netw, ctrl, c1, "Y", "7", 2)
	nodes[0].Down = true
	fmt.Println("  N0 crashes (Y=7 停留在未提交状态，两条复制消息滞留在网络中)")
	dump(nodes, ctrl)

	step("controller 选 N1 为 T2 主；另一客户端 c2 写 B=2 (req#1)，N1 全量同步 N2 后提交")
	ctrl.Elect(1, nodes, netw)
	c2 := &Client{ID: "c2", Cached: 1}
	write(nodes, netw, ctrl, c2, "B", "2", 1)
	dump(nodes, ctrl)

	step("N0 恢复重连：仍带着未提交的 Y=7(T1)；N1 全量同步它")
	nodes[0].Down = false
	SyncFollower(nodes[1], nodes[0], netw)
	fmt.Println("  N0 观察到 T2 -> 截断未提交的 Y=7，采纳 [A=1, B=2]")
	dump(nodes, ctrl)

	step("滞留的 T1 复制消息 (A=1, Y=7) 终于到达 -> 全部因任期过期被拒绝")
	netw.FlushDelayed(nodes)
	dump(nodes, ctrl)

	step("c1 仍缓存 N0，重试未完成的 req#2 (Y=7) -> 重定向到 N1 后提交（去重表随日志重建，req#2 > 已见的 req#1）")
	c1.Cached = 0
	resp := write(nodes, netw, ctrl, c1, "Y", "7", 2)
	if resp.Redirect >= 0 {
		c1.Cached = resp.Redirect
		write(nodes, netw, ctrl, c1, "Y", "7", 2)
	}
	dump(nodes, ctrl)
}

// ================= 场景 3：重复请求 —— 客户端重试与重复复制 =================

func scenarioDuplicate() {
	stepNum = 0
	fmt.Println("\n================ Scenario 3: 重复请求与幂等处理 ================")
	nodes, netw, ctrl := newCluster()
	c1 := &Client{ID: "c1", Cached: 0}

	step("c1 写 K=1 (req#1) -> 提交；模拟响应丢失（客户端未收到）")
	write(nodes, netw, ctrl, c1, "K", "1", 1)
	fmt.Println("  (response lost on the wire)")
	dump(nodes, ctrl)

	step("c1 重试 req#1 -> 去重表命中，返回缓存响应，日志不增长")
	before := len(nodes[0].Log)
	write(nodes, netw, ctrl, c1, "K", "1", 1)
	fmt.Printf("  N0 log length: %d -> %d (unchanged)\n", before, len(nodes[0].Log))
	dump(nodes, ctrl)

	step("c1 写 K=2 (req#2) -> 新条目；随后同一条复制消息重复投递到 N1 -> 幂等")
	write(nodes, netw, ctrl, c1, "K", "2", 2)
	last := nodes[0].Log[len(nodes[0].Log)-1]
	dup := ReplMsg{From: 0, To: 1, Term: last.Term, PrevIndex: len(nodes[0].Log) - 1,
		Entries: []Entry{last}, Commit: nodes[0].CommitIdx}
	before = len(nodes[1].Log)
	fmt.Printf("  re-delivering %s\n", dup)
	nodes[1].ReceiveReplication(dup)
	fmt.Printf("  N1 log length: %d -> %d (unchanged)\n", before, len(nodes[1].Log))
	dump(nodes, ctrl)

	step("故障切换：N1 成为 T2 主；c1 缓存的仍是 N0，发 req#3 (K=3)")
	ctrl.Elect(1, nodes, netw)
	resp := write(nodes, netw, ctrl, c1, "K", "3", 3)
	if resp.Redirect >= 0 {
		fmt.Println("  c1 更新缓存并重试同一 req#3")
		c1.Cached = resp.Redirect
		write(nodes, netw, ctrl, c1, "K", "3", 3)
	}
	dump(nodes, ctrl)

	step("c1 再次重试 req#3 -> N1 去重，日志不增长")
	before = len(nodes[1].Log)
	write(nodes, netw, ctrl, c1, "K", "3", 3)
	fmt.Printf("  N1 log length: %d -> %d (unchanged)\n", before, len(nodes[1].Log))
	dump(nodes, ctrl)
}
