# netsim — 固定路由网络容量验证器（离散事件模型）

对固定路由与流需求做容量验证：逐链路负载核算、冗余路径条件检查、故障场景容量
验证，并对每条超载链路输出**最小相关流集合**（移除后即可消除超载的最小流子集）。
所有包、队列和链路都在整数 tick 的离散事件模型中运行，不产生任何真实网络流量。

仅使用 Go 标准库。

## 运行

```sh
go run . -input examples/sample.json [-ticks N]
```

## 输入格式（JSON）

- `links`: `id`, `from`, `to`, `capacity`（包/tick）, `mode`:
  - `unidirectional`（默认）：仅 `from->to` 可用；
  - `shared`：双向可用，两个方向**共享**同一容量预算；
  - `separate`：双向可用，每个方向**各自独立**的容量预算。
- `flows`: `id`, `src`, `dst`, `demand`（包/tick，可为小数）, `route`（固定路由节点序列）。
- `failures`: 故障场景，声明失效的链路 `links` 列表。
- `simTicks`: 离散事件模拟的 tick 数（默认 200）。

## 验证规则

- 共享链路累加所有经过流（含双向共享模式下两个方向的流）。
- 不可达流（路由经过不存在或已失效的链路）被明确标记为 `UNREACHABLE`，
  **不会**被当作 0 负载成功；其负载不计入任何链路，模拟中也不注入包。
- 冗余路径条件：在基线拓扑中为每条流寻找与主路由**链路不相交**的备份路径
  （单向链路不可反向穿越）。
- 超载链路的最小相关流集合：按（子集大小, 移除总需求）字典序最小的流子集，
  移除后链路负载回落到容量以内。
- 离散事件模拟：源按需求速率注入包（小数速率用累加器取整），链路每 tick 按容量
  服务 FIFO 队列，包逐跳转发；末段积压持续增长判定为拥塞，与解析结果互相印证。

## 运行样例

输入 `examples/sample.json`：4 条链路（L1 双向共享、L2 双向分离、L3 单向、L4 双向
共享），4 条固定路由流，2 个故障场景（cut-L1、cut-L4）。输出（节选）：

```
=== Scenario: baseline ===
  Unreachable flows: none
  Link loads (5 capacity budgets):
    L1 [A<->B shared]            cap= 10.00 load=  8.00  ok
      flows: F1(5.00) F3(3.00)
    L2 [B->C separate]           cap=  8.00 load=  9.00  OVERLOAD excess=1.00
      flows: F1(5.00) F2(4.00)
      minimal overload flow set (remove to fix): {F2}
    ...
  Redundancy (link-disjoint backup path):
    F1: backup available: A->D->C
    F3: NO link-disjoint backup path
    ...
  Simulation (200 ticks, discrete event model):
    link L2|fwd       received=1800 forwarded=1600 maxBacklog=200 endBacklog=200  CONGESTED (backlog keeps growing)
    ...
=== Scenario: failure: cut-L1 (links down: L1) ===
  UNREACHABLE flow F1 (A->C demand=5.00): no usable link for hop A->B
  UNREACHABLE flow F3 (C->A demand=3.00): no usable link for hop B->A
  ...
```

完整输出见 `examples/sample_output.txt`。

## 测试

```sh
go test ./...
```

覆盖：共享/分离双向容量累加、不可达流不计为成功、故障场景重算、最小超载流集合
（单流与多流情形）、冗余路径（含单向链路不可反向）、模拟器欠载全送达/超载积压
增长/小数速率/共享链路合流/多跳与不可达流零注入。

测试结果（go1.11.4 darwin/amd64）：

```
ok  	netsim	6.336s   # 13 个测试全部 PASS
```
