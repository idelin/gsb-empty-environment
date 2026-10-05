# msftool — 最小生成森林替换边分析

对含整数权重、稳定边 ID 的无向图（允许平行边、自环、负权、非连通）：

1. 用 Kruskal 按 `(weight, id)` 全序求**规范最小生成森林**（同权按 ID 决胜，
   结果唯一确定）；
2. 对每条树边求移除后最便宜的替代边：每条非树边恰好覆盖其两端点在树中
   唯一路径上的所有树边，是该路径上每条树边的候选；逐树边取 `(weight, id)`
   最小者。候选不存在时标记为 `disconnected`。

## 输入（stdin）

```
n m
id u v w      # m 行；顶点 0..n-1，id 唯一，w 为整数
```

## 输出（stdout）

```
components <k>
total_weight <W>
edge <id> weight <w> -> replacement <id> weight <w>
edge <id> weight <w> -> disconnected
```

## 验证

`msf_test.go` 对小图枚举全部 `2^m` 个子集，求出规范 MSF（最小总权，
同权按 `(weight, id)` 字典序），并验证：

- 总权、连通分量数、树边集合与 Kruskal 完全一致；
- 对每条树边 `e`，在 `G - e` 上重新枚举求规范 MSF：分量数增加则必须为
  `disconnected`，否则边集差必须恰好是主算法给出的替代边，且权重差吻合。

固定用例覆盖平行边、同权决胜、自环、负权、桥（无替代）；另有 300 组
随机小图（1–7 顶点、0–16 边、大量同权与负权）全量对照。

## 运行

```sh
go test ./...
go build -o msftool . && ./msftool < input.txt
```
