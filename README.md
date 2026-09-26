# tracecp — 追踪 DAG 关键路径分析

在含并行子 span 与异步依赖的追踪 DAG 上计算关键路径，区分**墙钟耗时**（wall-clock）
与**累加工作量**（total work），并将**负时长**与**因果违例**作为异常单独报告。
纯 Go 标准库实现，无外部依赖；遥测事件为本地合成、可通过 `fixtures/` 下的 JSON
夹具回放，不接入任何监控平台。

## 语义约定

- **墙钟耗时**：`max(End) - min(Start)`，并行子任务不叠加——两个并行 10ms 子任务的
  父 span 墙钟仍是 10ms，而非 20ms。
- **累加工作量**：全部有效 span 时长之和，每个 span 只计一次（共享后继不重复计）。
- **关键路径**：解释端到端延迟的因果链（父子边 + 异步依赖边）。链上工作量按区间
  并集计算，嵌套 span 不重复计，因此单链上工作量不超过墙钟。
- **环依赖**：Kahn 拓扑排序检测，报告成环节点，**不输出伪造的关键路径**。
- **异常**（违例边被剪除、span 本身保留；负时长 span 整体剔除）：
  - `negative-duration`：`End < Start` 的 span；
  - `causality`：子 span 超出父 span 时间窗、依赖后继先于前驱结束、引用未知 span。

## 运行

```sh
go run ./cmd/tracecp fixtures/parallel.json
go test ./...
```

## 运行样例（实际输出）

```
== fixtures/parallel.json        # 两个并行 10ms 子任务
wall-clock: 10ms                 # 不是 20ms
total work: 30ms
critical path: root -> worker-a (wall 10ms, work 10ms)

== fixtures/shared_successor.json
wall-clock: 40ms
total work: 70ms                 # join 只计一次
critical path: root -> fetch-a -> join (wall 40ms, work 40ms)

== fixtures/async_chain.json     # 异步依赖跨空闲间隙延伸因果链
wall-clock: 100ms
total work: 160ms
critical path: root -> enqueue -> worker -> notify (wall 100ms, work 100ms)

== fixtures/cycle.json
critical path: unavailable (dependency cycle: a, b, c)   # 不给伪路径

== fixtures/anomalies.json
anomaly[negative-duration]: span broken-clock
anomaly[causality]: child "escapee" [10,60] escapes parent "root" [0,50]
anomaly[causality]: dependency "ok-a" -> "ok-b" violated: successor starts at 10 before predecessor ends at 15
```

## 测试结果

```
$ go test ./...
ok  	tracecp/internal/trace
```

覆盖：并行子任务墙钟/工作量区分、共享后继去重、异步依赖链、环检测不输出伪路径、
负时长与因果违例分类报告、空 trace 与单 span 退化情形。

## 布局

- `internal/trace/span.go` — span / 依赖 / trace 数据模型（JSON 夹具格式）
- `internal/trace/analyze.go` — 建图、校验、拓扑排序、关键路径 DP
- `internal/trace/report.go` — 报告结构
- `internal/trace/analyze_test.go` — go test 测试
- `cmd/tracecp/main.go` — 夹具回放 CLI
- `fixtures/*.json` — 本地合成的遥测夹具
