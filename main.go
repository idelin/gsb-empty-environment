package main

import "fmt"

func main() {
	// 丢包率 35%，固定种子保证可复现。
	c := NewCluster(7, 0.35)
	for _, id := range []NodeID{"alpha", "bravo", "charlie", "delta"} {
		c.Join(id, 64)
	}

	fmt.Println("== 阶段 1: 四个节点在线，alpha/bravo 丢包广播 ==")
	broadcastN(c, "alpha", 8)
	broadcastN(c, "bravo", 5)
	fmt.Printf("丢失的投递次数: %d\n", c.Lost())
	printWindows(c)

	fmt.Println("\n== 阶段 2: charlie 退出（带着未修复的缺口离线），其余节点修复并压缩 ==")
	c.Leave("charlie")
	repairUntilStable(c)
	c.ReportAcks()
	compressAll(c)
	printFloors(c)

	fmt.Println("\n== 阶段 3: charlie 离线期间 alpha 继续广播 ==")
	broadcastN(c, "alpha", 4)
	repairUntilStable(c)
	c.ReportAcks()
	compressAll(c)
	printFloors(c)

	fmt.Println("\n== 阶段 4: charlie 重入（新代次），拒绝沿用退出前的确认 ==")
	n := c.Rejoin("charlie")
	fmt.Printf("charlie 重入，当前代次 epoch=%d\n", n.Epoch())
	if err := c.RecordAck("charlie", 1, "alpha", 999); err != nil {
		fmt.Printf("重放退出前的确认被拒绝: %v\n", err)
	}
	floor, ok := c.CompressionFloor("alpha")
	fmt.Printf("重放后 alpha 压缩水位: floor=%d ready=%v（未被脏确认抬高）\n", floor, ok)

	accepted, dups := c.CatchUp("charlie")
	fmt.Printf("charlie 补传完成: 新接收 %d 条，识别并丢弃旧消息 %d 条\n", accepted, dups)
	c.ReportAcks()
	compressAll(c)
	printFloors(c)

	fmt.Println("\n== 阶段 5: 最终状态与不变量校验 ==")
	repairUntilStable(c)
	c.ReportAcks()
	compressAll(c)
	printWindows(c)
	verify(c)
}

func broadcastN(c *Cluster, source NodeID, n int) {
	for i := 0; i < n; i++ {
		if _, err := c.Broadcast(source, fmt.Sprintf("%s#%d", source, i+1)); err != nil {
			panic(err)
		}
	}
	fmt.Printf("%s 广播了 %d 条消息\n", source, n)
}

func repairUntilStable(c *Cluster) {
	total := 0
	for {
		healed := c.RepairRound()
		total += healed
		if healed == 0 {
			break
		}
	}
	fmt.Printf("修复轮次结束，共补传 %d 条\n", total)
}

func compressAll(c *Cluster) {
	for _, id := range []NodeID{"alpha", "bravo", "charlie", "delta"} {
		if c.MaxSeq(id) == 0 {
			continue
		}
		if floor, advanced := c.MaybeCompress(id); advanced {
			fmt.Printf("源 %s 的去重历史压缩到水位 %d（所有有效订阅者已确认）\n", id, floor)
		}
	}
}

func printFloors(c *Cluster) {
	for _, id := range c.Members() {
		n := c.Node(id)
		fmt.Printf("  成员 %s (epoch=%d): alpha水位=%d bravo水位=%d\n",
			id, n.Epoch(), n.AckFloor("alpha"), n.AckFloor("bravo"))
	}
}

func printWindows(c *Cluster) {
	for _, id := range c.Members() {
		n := c.Node(id)
		for _, src := range []NodeID{"alpha", "bravo"} {
			w := n.Window(src)
			fmt.Printf("  %s <- %s: 水位=%d 缺口=%v 压缩底线=%d 重复=%d\n",
				id, src, w.Watermark(), w.Gaps(), w.Floor(), n.Duplicates())
		}
	}
}

// verify checks the simulator invariants and panics on violation.
func verify(c *Cluster) {
	for _, src := range []NodeID{"alpha", "bravo"} {
		maxSeq := c.MaxSeq(src)
		// 1. 所有有效订阅者都完整收到了 1..maxSeq。
		minAck := maxSeq
		for _, id := range c.Members() {
			n := c.Node(id)
			if got := n.AckFloor(src); got != maxSeq {
				panic(fmt.Sprintf("不变量违反: %s 对源 %s 的水位=%d, 期望=%d", id, src, got, maxSeq))
			}
			if ack := n.AckFloor(src); ack < minAck {
				minAck = ack
			}
		}
		// 2. 集群压缩水位不超过任何有效订阅者的确认水位。
		if f := c.Floor(src); f > minAck {
			panic(fmt.Sprintf("不变量违反: 源 %s 压缩水位 %d 超过成员确认底线 %d", src, f, minAck))
		}
		// 3. 已建立的压缩水位最终落到每个成员的去重窗口上。
		for _, id := range c.Members() {
			if f := c.Node(id).Window(src).Floor(); f != c.Floor(src) {
				panic(fmt.Sprintf("不变量违反: %s 对源 %s 的窗口压缩底线=%d, 集群水位=%d", id, src, f, c.Floor(src)))
			}
		}
		fmt.Printf("校验通过: 源 %s 全部 %d 条消息被所有有效订阅者确认，压缩水位=%d\n",
			src, maxSeq, c.Floor(src))
	}
	if c.Node("charlie").Epoch() != 2 {
		panic("不变量违反: charlie 重入后代次未递增")
	}
	fmt.Println("校验通过: charlie 重入使用新代次，退出前的确认代次未被沿用")
}
