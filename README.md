# skewcheck — 因果约束时钟偏移区间推导

根据跨节点“请求发送 → 对端接收 / 响应发送 → 本端接收”的**因果约束**，推导各节点
相对参考节点的时钟偏移可行区间；当约束互相矛盾时给出**冲突链**（负环），而不是
把负耗时强行截成 0。

遥测事件全部在**本地夹具（JSON）合成并由测试/CLI 回放**：不接任何监控/APM
平台，也没有纯展示看板。仅依赖 Go 标准库。

## 模型

每个节点 `n` 有一个相对参考节点 `R` 的时钟偏移 `x_n`（`x_R = 0`）：

```
真实时间(事件) = 本地观测时间戳(事件) + x_n
```

因果边 `u@t_u → v@t_v`（发送事件 `t_u`，接收事件 `t_v`）要求结果不先于原因：

```
t_u + x_u ≤ t_v + x_v
  ⇔  x_u − x_v ≤ Δ,      Δ = t_v − t_u   （有符号，永不截断）
```

这是标准的**差分约束系统**：

- 约束 `x_u − x_v ≤ Δ` 对应约束图上的边 `v → u`，权重 `Δ`。
- 以参考节点为源做 Bellman-Ford：`d[v] = min ΣΔ` 给出偏移上界；
  在反图上跑一遍给出下界，组合成每个节点的闭区间 `[Lo, Hi]`。
- 存在**负环** ⇔ 约束矛盾（例如负 RTT）。用“连接所有节点、距离 0 的虚拟源”跑
  Bellman-Ford 可发现任意位置的负环，并沿前驱链还原成按因果方向（发送→接收）
  排序的**冲突链**；同一环的不同旋转只报一次。
- 与参考节点之间没有任何因果路径的节点，两侧都不可达，返回**宽区间**
  `[-wide, +wide]`（默认 ±1 年，`-wide` 可调）。
- 可行时用虚拟源的距离势（平移使 `x_R=0`）作为一组**可行偏移**，据此得到的
  校正时间戳保证：每条因果边不倒序，节点内事件的本地先后也不倒序
  （`MinSlack ≥ 0`）。

`Δ` 按有符号数参与计算：负 `Δ` 是合法且有意义的紧约束；只有闭合的负 `ΣΔ`
（负环）才是矛盾。

## 目录

```
skew/                 核心：图模型 + Bellman-Ford 区间/冲突链推导（标准库）
  types.go            Event / Edge / Graph
  analyze.go          Analyze()、隐式本地顺序边、区间、可行偏移、冲突链装配
  bellman.go          饱和算术 + Bellman-Ford（前向/反向/虚拟源负环提取）
fixture/              本地 JSON 夹具：加载、校验、按观测时间戳回放
cmd/skewcheck/        命令行分析器（文本或 -json），矛盾时退出码 1
fixtures/             样例夹具、运行样例输出、测试结果
  feasible_rtt.json       可行往返（有界区间）
  contradiction_rtt.json  矛盾往返（负环，返回冲突链）
  disconnected.json       缺约束（宽区间）
  negative_delta.json     单边负观测耗时（单侧紧约束，不截断）
  sample_output.txt       上述夹具的实际运行输出
  test_results.txt        go test -race -v 的实际结果
```

## 夹具格式

```json
{
  "reference": "client",
  "wide": "87600h",
  "events": [
    {"id": "c_req", "node": "client", "ts": 1000},
    {"id": "s_req", "node": "server", "ts": "20ms"}
  ],
  "edges": [{"from": "c_req", "to": "s_req"}]
}
```

- `ts`：整数纳秒，或 `time.ParseDuration` 相对时长字符串（如 `"20ms"`、`"-1ms"`）。
- `edges`：显式因果边，按事件 ID 引用。
- 同一节点上按观测时间戳相邻的事件，会自动补“本地先后”约束（正自环），
  因此节点内倒序也会作为负环报出。

## 运行样例

```bash
go run ./cmd/skewcheck fixtures/feasible_rtt.json        # 可行，退出码 0
go run ./cmd/skewcheck fixtures/contradiction_rtt.json   # 冲突链，退出码 1
go run ./cmd/skewcheck -wide 1s fixtures/disconnected.json
go run ./cmd/skewcheck -json fixtures/contradiction_rtt.json
```

可行往返（server 真实偏移区间 `[-100ns, -80ns]`，校正后因果保序）：

```
  server   [-100ns, -80ns] (ns: [-100, -80])
  minimum corrected causal duration (slack): 0s
  c_req client@1000 -> 1000
  s_req server@1100 -> 1020
  s_resp server@1120 -> 1040
  c_resp client@1040 -> 1040
```

矛盾往返（Δ 依次为 −10、+5，和为 −5 < 0；负耗时 −10ns 原样保留）：

```
INFEASIBLE: 1 contradiction chain(s)
chain 1: total signed duration -5ns (negative => contradiction)
  client@1000 -> server@990   weight = -10ns
  server@1005 -> client@1010  weight = 5ns
```

缺约束（与参考节点无因果路径，宽区间）：

```
  island   [-infty, +infty] (ns: [-31536000000000000, 31536000000000000])
```

完整输出见 `fixtures/sample_output.txt`。

## 测试

```bash
go test ./...            # 全部测试
go test -race -v ./...   # 竞态检测 + 详细结果
go vet ./...
```

测试覆盖（均为标准库 `go test`）：

- 可行往返区间数值正确，参考节点钉在 `[0,0]`；
- 矛盾往返返回单条冲突链、权重和为负、边按因果方向排序且**保留负 Δ**；
- 单边负 `Δ` 产生单侧紧下界而**不是**截成 0；零 Δ 可行；
- 无连接节点返回宽区间；`-wide` 可调；
- 校正后所有显式因果边不倒序、节点内本地顺序不倒序、`MinSlack ≥ 0`；
- 合成“真实偏移”场景：所有真实偏移都落在推导出的区间内；
- 节点内倒序的显式边触发负自环；坏夹具（空 reference、重复事件、
  悬空边、坏时间戳、缺参考节点）报错；
- CLI 端到端：可行退出码 0、矛盾退出码 1、JSON 输出可解析。

最近一次 `go test -race -v ./...` 全部通过，记录在 `fixtures/test_results.txt`。
