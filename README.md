# causalrecon — 跨时区事件关联与因果图重构器

离散分布式追踪日志的时间因果拓扑重构器（Go，仅标准库）。

不同服务器的物理时钟存在未知漂移。分析器利用 RPC 请求/响应的
发送/接收四元组（`SendRequest / RecvRequest / SendResponse / RecvResponse`）
建立 Lamport happens-before 偏序图，将“消息必须先发送后到达”转化为
各服务器时钟基线 offset 的差分约束，用 Bellman-Ford 最长路求解最小校正量，
纠正“负延迟”这类物理上荒谬的反例，输出物理自洽的调用拓扑图。

## 算法

- 因果边：同一 RPC 的 `SEND_REQ -> RECV_REQ`、`SEND_RESP -> RECV_RESP`（网络边），
  以及同一服务器上按本地时钟排序的程序序边。
- 约束：对每条跨机边 `u -> v`，要求 `corrected(v) >= corrected(u) + minLatency`，
  其中 `corrected(e) = Raw(e) + offset[server(e)]`，即
  `offset[v.srv] - offset[u.srv] >= Raw(u) + minLatency - Raw(v)`。
- 求解：Bellman-Ford 最长路（初值全 0 等价于超级源点），得到最小校正解；
  若存在正权重环则判定日志自相矛盾，返回 `ErrInfeasible`。

## 运行样例

```
$ go run ./cmd/demo
=== 校正前（原始物理时钟读数） ===
  a1   srv=A rpc=rpc-1  SEND_REQ  raw=12:00:00.000
  b1   srv=B rpc=rpc-1  RECV_REQ  raw=11:59:59.000
  b2   srv=B rpc=rpc-1  SEND_RESP raw=11:59:59.600
  a2   srv=A rpc=rpc-1  RECV_RESP raw=12:00:00.800
  >> 请求网络延迟（原始读数直接相减）: -1s  <-- 负延迟，物理上荒谬

=== 时钟基线校正量 ===
  srv=A offset=0s
  srv=B offset=1.001s

=== 校正后：物理自洽的调用拓扑（按校正时间排序） ===
  a1   srv=A rpc=rpc-1  SEND_REQ  corrected=12:00:00.000
  b1   srv=B rpc=rpc-1  RECV_REQ  corrected=12:00:00.001
  b2   srv=B rpc=rpc-1  SEND_RESP corrected=12:00:00.601
  a2   srv=A rpc=rpc-1  RECV_RESP corrected=12:00:00.800

=== 因果边（happens-before）校验 ===
  a1(A) -> b1(B)  network       Δ=1ms
  b2(B) -> a2(A)  network       Δ=199ms
  a1(A) -> a2(A)  program-order Δ=800ms
  b1(B) -> b2(B)  program-order Δ=600ms
  OK: 所有因果边均满足 物理时序自洽（跨机边 Δ >= 1ms）

=== rpc-1 校正后时延 ===
  请求网络延迟=1ms  响应网络延迟=199ms  端到端=800ms
```

验收边界复现：A 日志 12:00:00 发请求、B 日志 11:59:59 收请求（时钟倒流）；
算法强制将 B 的时钟基线校正 `+1.001s`，负延迟 `-1s` 被纠正为 `+1ms`，
输出物理自洽的调用拓扑。

## 测试结果

```
$ go test ./... -v
=== RUN   TestNegativeLatencyCorrected        --- PASS   (验收边界：负延迟纠正)
=== RUN   TestConsistentClocksUnchanged       --- PASS   (一致时钟不被误改)
=== RUN   TestMultiServerChain                --- PASS   (A->B->C 漂移沿链传播)
=== RUN   TestResponseNegativeLatencyCorrected--- PASS   (响应方向负延迟纠正)
=== RUN   TestInfeasibleLogsDetected          --- PASS   (矛盾日志报 ErrInfeasible)
=== RUN   TestUnpairedRPCRejected             --- PASS   (残缺四元组报错)
PASS
ok      causalrecon
```

## 文件

- `causal.go` — 核心库：事件/因果图/差分约束求解/拓扑输出
- `causal_test.go` — go test 测试
- `cmd/demo/main.go` — 验收场景演示
