# causalgraph — 跨时区事件关联与因果图重构器

离散分布式追踪日志的时间因果拓扑重构器（Go，仅标准库）。

## 问题

不同服务器的物理时钟存在未知漂移，且日志可能使用不同时区记录。
直接比较物理时间戳会出现荒谬的**负延迟反例**：

```
serverA 12:00:00.000 SEND msg-1 ──► serverB 11:59:59.100 RECV msg-1   ← 物理时钟倒流
```

## 算法

1. **事件关联**：按 RPC 消息 ID 配对 send/recv 四元组，建立跨机因果边
   `send → recv`；同一服务器上的相邻日志建立本地程序序边。
2. **差分约束**：对每条 RPC 边建立约束
   `corrected(recv) ≥ corrected(send) + minLatency`，即
   `offset[to] ≤ offset[from] + (recvTime − sendTime) − minLatency`。
3. **求解**：引入虚拟基准节点（向每台服务器连权重 0 的边，表达
   “无因果证据则不校正”的最小干预先验），用 Bellman-Ford 求满足全部
   约束的最大偏移解（差分约束系统的最短路径解）。
4. **一致性检查**：约束系统存在负环 ⇒ 日志因果不自洽，返回错误。
5. **输出**：每台服务器的时钟基线校正量、校正后的调用拓扑
   （统一 UTC 时间轴）、各 RPC 延迟、Graphviz DOT 图。

校正规则：`corrected(t) = raw(t) − offset[server]`。
只有出现负延迟反例时，偏移量才会被因果约束强制拉为负值。

## 运行样例

```sh
go run ./cmd/reconstruct
```

输出（节选）：

```
== 时钟基线校正量 (corrected = raw - offset) ==
  serverA    offset = 0s
  serverB    offset = -901ms
  serverC    offset = -1.802s

== 校正前负延迟反例 ==
  msg msg-1: serverA SEND 12:00:00.000 -> serverB RECV 11:59:59.100 (物理倒流 900ms)
  msg msg-2: serverB SEND 11:59:59.200 -> serverC RECV 03:59:58.300 (物理倒流 900ms)

== 校正后调用拓扑（按校正时间排序，UTC） ==
  04:00:00.000  serverA    SEND msg=msg-1  peer=serverB
  04:00:00.001  serverB    RECV msg=msg-1  peer=serverA
  04:00:00.101  serverB    SEND msg=msg-2  peer=serverC
  04:00:00.102  serverC    RECV msg=msg-2  peer=serverB
  ...
校验通过: 校正后无负延迟，因果偏序物理自洽。
```

## 日志格式

每行一条事件，时间戳为 RFC3339（可带任意时区）：

```
<RFC3339时间戳> <服务器> <SEND|RECV> <消息ID> <对端服务器>
```

## 测试

```sh
go test ./... -v
```

覆盖：验收场景（B 时钟倒流 1s 被强制校正）、跨时区三机链式漂移、
因果不自洽负环检测、未配对消息、日志解析、无漂移零校正、报告/DOT 输出。

## 文件

- `causal.go` — 因果图重构核心（约束构建、Bellman-Ford 求解、报告/DOT）
- `parse.go` — 追踪日志解析
- `causal_test.go` — 测试
- `cmd/reconstruct/main.go` — 运行样例
