# beatfit — 节拍网格拟合器

从含**漏拍**和**多余敲击**的时间序列中，在有限候选集（整数周期 × 起点）内
搜索最优节拍网格。目标函数：

    min  Σ|敲击时刻 − 网格点|   (匹配偏差)
       + missPenalty  × 漏拍数   (无敲击的网格点)
       + extraPenalty × 多余点数 (无网格点的敲击)

匹配**保持顺序**（敲击与网格点均单调递增）。对全部候选做穷举搜索，
不做相邻时间差投票，因此差值众数失效时（如每拍后紧跟一个多余点）
仍能找到真网格。最优代价相同的**全部周期**都会输出。

## 算法

对每个候选 `(period, offset)`，网格点为 `offset + k·period`。在敲击序列
与网格点序列之间做编辑距离式 DP（匹配 / 跳过敲击 / 跳过网格点），
回溯得到对应关系。网格点只保留落在观测范围 ±(miss+extra) 内的点
（偏差超过跳两侧代价的匹配绝不占优），保证 DP 规模最小。

- 库：`fit.go` — `FitAll`（全局搜索 + 并列周期）、`FitCandidate`（单网格对齐）、
  `IntegerCandidates`（生成 p∈[pMin,pMax]、offset∈[0,p) 的全部候选）
- CLI：`cmd/beatfit` — 从 stdin 读 JSON，向 stdout 写 JSON

## 使用

```sh
go test ./...
go build -o beatfit ./cmd/beatfit
echo '{"times":[0,1,10,11,20,21,30],"periodMin":2,"periodMax":20,
      "missPenalty":2,"extraPenalty":2}' | ./beatfit
```

输入字段：`times`（秒，可乱序）、`periodMin`/`periodMax`（整数周期范围），
或显式 `candidates: [{"period":10,"offset":0}, ...]`（优先级更高）；
`missPenalty`、`extraPenalty` 为罚分。

输出：`bestCost`、`periods`（全部并列最优周期）、`solutions`（每个并列周期的
最优解：`matches` 含 tapIndex/beat/偏差，`extraTaps` 为多余敲击下标，
`missedBeats` 为漏拍的网格序号 k）。
