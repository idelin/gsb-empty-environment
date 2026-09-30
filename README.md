# netverify

固定路由 + 流需求的网络容量验证工具。全部在**整数 tick 的离散事件模型**中运行，
不产生任何真实网络流量。仅使用 Go 标准库。

## 功能

- **每链路负载校验**：共享链路累加所有经过流的需求；双向容量按输入声明为
  `separate`（每方向独立容量）或 `shared`（两方向共享一份容量）。
- **冗余路径条件**：对每条流检查拓扑中是否存在与其固定路由**链路不相交**的备份路径。
- **故障场景容量**：对每个声明的故障场景，剔除经过故障链路的流后重新核算容量。
  路由被切断的流标记为 `UNREACHABLE`（计为失败），**绝不计作 0 负载成功**。
- **超载最小相关流集合**：对每条超载链路，枚举包含意义下最小的流子集——
  子集需求之和超过容量，但去掉其中任意一条流即恢复到容量之内
  （流数过多时退化为贪心最小集合并标注 truncated）。
- **离散事件仿真**：包按需求速率（支持小数速率，整数累加器发包）注入，
  链路每 tick 按容量服务 FIFO 队列，共享模式两方向轮转竞争同一份容量令牌；
  输出每流 delivered/backlog 与每链路队列增长（拥塞判定），与静态分析互相印证。

## 输入格式（JSON）

```json
{
  "name": "diamond",
  "links": [
    {"id": "AB", "from": "A", "to": "B", "capacity": 10, "bidirectional": "separate"},
    {"id": "BC", "from": "B", "to": "C", "capacity": 4,  "bidirectional": "shared"}
  ],
  "flows": [
    {"id": "F1", "src": "A", "dst": "B", "demand": 6, "path": ["AB"]}
  ],
  "failureScenarios": [
    {"id": "S1", "failedLinks": ["AB"]}
  ],
  "simulation": {"ticks": 500}
}
```

- `capacity` / `demand` 单位均为 包/tick（可为小数）。
- `path` 为固定路由的有序链路 ID 列表；构建时校验相邻链衔接且最终到达 `dst`，
  方向（fwd/rev）自动解析。

## 运行

```sh
go run . -input examples/diamond.json            # 静态校验 + 仿真
go run . -input examples/diamond.json -no-sim    # 仅静态校验
go run . -input examples/diamond.json -ticks 1000
```

基线存在超载时进程以退出码 1 结束。

## 运行样例（节选）

```
network "diamond": 6 links, 6 flows, 2 failure scenarios

== baseline (no failures) ==
  link AB     fwd     cap=  10.0  load=  12.0  util=120.0%  OVERLOAD  flows=[F1 F6]
    minimal overload flow sets (removing any one flow restores capacity):
      {F1, F6}
  link BC     shared   cap=   4.0  load=   6.0 (fwd=3.0 rev=3.0)  util=150.0%  OVERLOAD
    minimal overload flow sets (removing any one flow restores capacity):
      {F3, F4}

== redundancy check (link-disjoint backup path exists) ==
  flow F1     REDUNDANT   alt path: AC -> CD
  flow F5     NOT redundant (no link-disjoint alternate route)

== scenario S1: failed links [AB] ==
  UNREACHABLE flows (route broken, counted as FAILED, not 0-load OK): F1, F6
  ...

== discrete-event simulation: baseline (500 ticks) ==
  flow F1     generated=3000   delivered=2490   backlog=510    STARVED (backlog grows)
  link AB     fwd  maxQ=998    endQ=998    served=4990    CONGESTED (queue still growing at end)
  link BC     fwd  maxQ=499    endQ=499    served=998     CONGESTED (queue still growing at end)
  link BC     rev  maxQ=499    endQ=499    served=998     CONGESTED (queue still growing at end)
```

静态判定超载的链路（AB、BC 双向）在仿真中均表现为队列持续增长，两种方法结论一致。

## 测试

```sh
go test ./...
```

覆盖：路由方向解析与非法输入拒绝、独立/共享两种双向容量的负载累加、
最小超载流集合枚举与贪心退化、不可达流不计成功、冗余路径判定、
仿真欠载全交付/过载积压增长/共享容量令牌守恒/小数速率/多跳交付。

测试结果：

```
ok  	netverify
```
