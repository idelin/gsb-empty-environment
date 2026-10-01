// demo 复现验收场景：发布新集群配置 -> 10% 灰度 -> 验证读写 ->
// 以 10% 步长推移至 100%，全程校验无丢包。
package main

import (
	"fmt"
	"os"

	"routemig"
)

func mustTopology(cfg routemig.ClusterConfig) *routemig.Topology {
	t, err := routemig.NewTopology(cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "build topology:", err)
		os.Exit(1)
	}
	return t
}

func main() {
	// 1. 初始：旧拓扑 Top_Old（4 节点）承载全部流量。
	oldCfg := routemig.ClusterConfig{Name: "Top_Old"}
	for i := 0; i < 4; i++ {
		oldCfg.Nodes = append(oldCfg.Nodes, routemig.NodeSpec{
			ID:   fmt.Sprintf("old-node-%d", i),
			Addr: fmt.Sprintf("10.0.0.%d:7000", i),
		})
	}
	c := routemig.NewController(mustTopology(oldCfg))

	// 存量数据（迁移前写入旧拓扑）。
	const preexisting = 2000
	expected := make(map[string]string)
	for i := 0; i < preexisting; i++ {
		key, val := fmt.Sprintf("user:%d", i), fmt.Sprintf("profile-v1-%d", i)
		c.Write(key, val)
		expected[key] = val
	}
	fmt.Printf("[init] Top_Old serving 100%% traffic, %d existing keys\n", len(expected))

	// 2. 发布新集群配置 Top_New（6 节点），进入双拓扑共存。
	newCfg := routemig.ClusterConfig{Name: "Top_New"}
	for i := 0; i < 6; i++ {
		newCfg.Nodes = append(newCfg.Nodes, routemig.NodeSpec{
			ID:   fmt.Sprintf("new-node-%d", i),
			Addr: fmt.Sprintf("10.1.0.%d:7000", i),
		})
	}
	c.PublishNewTopology(mustTopology(newCfg))
	fmt.Println("[publish] Top_New cluster config published, dual-topology mode on")

	// 3. 设定 10% 灰度：10% 新键路由至 Top_New，90% 保持 Top_Old。
	if err := c.SetGrayPercent(10); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("[gray] set gray percent to 10%")

	lost := 0
	verify := func(stage string) {
		for key, want := range expected {
			got, ok, _ := c.Read(key)
			if !ok || got != want {
				lost++
				fmt.Printf("  LOST [%s] key=%s got=(%q,%v) want=%q\n", stage, key, got, ok, want)
			}
		}
	}

	// 4. 验证新集群读写正常：向两侧写入新键并全部读回校验。
	const perStage = 500
	writeStage := func(stage int) {
		for i := 0; i < perStage; i++ {
			key, val := fmt.Sprintf("event:s%d:%d", stage, i), fmt.Sprintf("payload-s%d-%d", stage, i)
			c.Write(key, val)
			expected[key] = val
		}
	}
	writeStage(0)
	verify("gray=10%")
	// 统计本阶段新键的灰度命中比例（存量键已固化在 Top_Old，不参与统计）。
	stageNew := 0
	for i := 0; i < perStage; i++ {
		if v, _ := c.KeyVersion(fmt.Sprintf("event:s0:%d", i)); v == routemig.VersionNew {
			stageNew++
		}
	}
	fmt.Printf("[verify] gray=10%%: %.1f%% of new keys routed to Top_New (%d/%d), reads verified, lost=%d\n",
		float64(stageNew)/perStage*100, stageNew, perStage, lost)

	// 5. 以 10% 步长逐步推移至 100%，每步写入新流量并全量校验。
	for gray := 20; gray <= 100; gray += 10 {
		if _, err := c.StepUp(10); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		writeStage(gray / 10)
		verify(fmt.Sprintf("gray=%d%%", gray))
		fmt.Printf("[gray] shifted to %3d%%: cumulative keys=%d, lost=%d\n",
			c.GrayPercent(), len(expected), lost)
	}

	// 6. 汇总。
	m := c.Metrics()
	dist := c.VersionDistribution()
	fmt.Println("--- migration summary ---")
	fmt.Printf("total requests : %d (reads=%d writes=%d)\n", m.Reads+m.Writes, m.Reads, m.Writes)
	fmt.Printf("routed         : Top_Old=%d Top_New=%d\n", m.OldRouted, m.NewRouted)
	fmt.Printf("key directory  : %d pinned (old=%d new=%d)\n",
		c.DirectorySize(), dist[routemig.VersionOld], dist[routemig.VersionNew])
	fmt.Printf("Top_New nodes  : %v\n", c.NodeLoad(routemig.VersionNew))
	fmt.Printf("lost packets   : %d\n", lost)

	if lost > 0 {
		fmt.Println("RESULT: FAIL")
		os.Exit(1)
	}
	fmt.Println("RESULT: PASS — migration 10% -> 100% completed with zero loss")
}
