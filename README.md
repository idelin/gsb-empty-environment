# mapmatch

轨迹采集服务收到的GPS点有漂移，需要还原车辆实际走过的道路。给Go轨迹服务实现地图匹配包：以道路图为输入，按距离给出候选路段，用隐马尔可夫模型和Viterbi选择路径，转移概率比较路网最短距离与直线距离；同一路段上倒退超过阈值视为掉头，相邻点间隔超过时间阈值时分段；流式输入，窗口确定后输出。用在已知路线上加噪生成的轨迹测试匹配正确率。

## 用法

```go
g := mapmatch.NewGraph()
g.AddNode(1, 31.0, 121.0)
g.AddNode(2, 31.0, 121.001)
g.AddEdge(10, 1, 2, []mapmatch.LatLon{{Lat: 31.0, Lon: 121.0}, {Lat: 31.0, Lon: 121.001}})
g.AddEdge(11, 2, 1, []mapmatch.LatLon{{Lat: 31.0, Lon: 121.001}, {Lat: 31.0, Lon: 121.0}})

m := mapmatch.NewMatcher(g, mapmatch.DefaultConfig())
for _, p := range points { // 流式输入 GPS 点
	for _, r := range m.Add(p) { // 窗口确定或分段时输出
		_ = r.EdgeID
	}
}
tail := m.Flush() // 轨迹结束，冲刷剩余结果
```

## 结构

- `graph.go`：道路图（节点/有向路段）、折线投影、Dijkstra 最短路
- `index.go`：经纬度格网空间索引，按距离检索候选路段
- `matcher.go`：HMM/Viterbi 流式匹配器（发射概率用高斯距离，转移概率比较路网最短距离与直线距离；同路段倒退超阈值视为掉头；超时阈值分段；滑窗提交输出）
- `mapmatch_test.go`：网格路网上已知路线加噪轨迹的正确率测试，及掉头、分段、流式窗口测试

运行测试：`go test ./...`
