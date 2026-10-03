# causal-trace-reconstructor

跨时区事件关联与因果图重构器：面向离散分布式追踪日志的时间因果拓扑重构。
各服务器物理时钟存在未知漂移时，利用 RPC 请求/响应的发送/接收四元组建立
happened-before 偏序图，检测并纠正荒谬的负延迟反例，输出物理自洽的调用拓扑图。

## 原理

1. **因果图构建**（`src/graph.ts`）：同一服务器内按本地物理时序连 process 边；
   按 `rpcId` 配对 `REQ_SEND -> REQ_RECV`、`RESP_SEND -> RESP_RECV` 连 message 边，
   得到 Lamport 偏序图。
2. **反例检测**（`src/reconstructor.ts`）：message 边上 `recv - send < 0` 即为
   物理时钟倒流产生的负延迟反例。
3. **时钟基线校正**（`src/corrector.ts`）：对每条 message 边建立差分约束
   `offset[B] >= offset[A] + send + L - recv`（L 为最小网络延迟，默认 1ms），
   用 Bellman-Ford 松弛求最小不动点解——只把接收方时钟基线向前推，
   发送方永不回退，校正最小且确定。
4. **拓扑输出**：按校正后时间重盖时间戳，Kahn 算法输出偏序的拓扑序。

时区与漂移被分开处理：ISO-8601 时间戳先归一化到 epoch 毫秒（消除时区差异），
剩余的时钟漂移由因果约束校正。

## 运行

需要 Node.js >= 23.6（原生 TypeScript type-stripping，零依赖）：

```sh
npm run demo    # 运行验收样例
npm test        # node:test 内置测试
```

## 验收样例（demo/sample.ts）

服务器 A 日志 `12:00:00 (+08:00)` 发出请求，服务器 B 日志 `11:59:59`（等效 UTC
`03:59:59.000Z`）收到请求——物理时钟倒流。算法强制校正 B 的时钟基线 `+1001ms`，
链路 B -> C 的漂移 `+1032ms` 也被传递校正：

```
=== negative-latency counterexamples (raw physical clocks) ===
  rpc=order-1 server-A -> server-B  ... latency=-1000ms  <-- impossible
  rpc=db-1 server-B -> server-C  ... latency=-30ms  <-- impossible

=== forced clock-baseline corrections ===
  server-A: offset +0ms
  server-B: offset +1001ms
  server-C: offset +1032ms

=== physically self-consistent causal topology (topological order) ===
  2026-10-04T04:00:00.000Z  server-A  REQ_SEND  rpc=order-1
  2026-10-04T04:00:00.001Z  server-B  REQ_RECV  rpc=order-1
  2026-10-04T04:00:00.011Z  server-B  REQ_SEND  rpc=db-1
  2026-10-04T04:00:00.012Z  server-C  REQ_RECV  rpc=db-1
  2026-10-04T04:00:00.072Z  server-C  RESP_SEND rpc=db-1
  2026-10-04T04:00:00.091Z  server-B  RESP_RECV rpc=db-1
  2026-10-04T04:00:00.101Z  server-B  RESP_SEND rpc=order-1
  2026-10-04T04:00:00.120Z  server-A  RESP_RECV rpc=order-1
```

## 测试结果

```
✔ acceptance: A sends 12:00:00, B receives 11:59:59 -> B baseline forced forward
✔ clock drift is distinct from timezone: same instant in two zones needs no correction
✔ chain A -> B -> C propagates corrections transitively
✔ topological order respects happened-before on every edge
✔ one server gets exactly one baseline shared across all its RPCs
✔ parser normalises timezone offsets to the same instant
✔ parser rejects malformed lines
✔ estimateClockOffsets never moves senders backward
ℹ tests 8  ℹ pass 8  ℹ fail 0
```

## 日志格式

每行一个事件，时间戳可带任意时区偏移：

```
2026-10-04T12:00:00.000+08:00 [server-A] rpc=order-1 REQ_SEND
```

事件类型：`REQ_SEND` / `REQ_RECV` / `RESP_SEND` / `RESP_RECV`。
