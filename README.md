# taskdag — 并发 DAG 任务调度器（Go 标准库）

纯标准库实现的依赖任务调度器，无第三方依赖。

## 调度语义

- **全部前驱成功才运行**：任务在所有依赖（deps）均为 `SUCCEEDED` 后才启动；就绪任务并发执行。
- **失败传播阻塞**：前驱 `FAILED`/`BLOCKED` 时，后继标记为 `BLOCKED`，阻塞沿图传递。
- **独立分支继续**：与失败无关的分支不受影响，继续并发运行。
- **运行前环检测**：`Run` 先做缺依赖与有环校验，有环立即返回 `*dag.CycleError`
  （`Chain` 为闭合节点链，首尾相同，如 `[a c b a]`），**不启动任何任务、不挂起等待**。
- **动态加边受限规则**：`AddDependency(from, to)` 表示 `from` 依赖 `to`，要求
  - 两端节点均已注册（否则 `ErrNotFound`）；
  - `from` 尚未开始、未进入终态（否则 `ErrTaskNotPending`）；
  - 新边不得成环（否则返回 `*CycleError`）；
  - 重复边幂等返回 `nil`。
  可在 `Run` 前调用，也可在 `Run` 执行期间从其他 goroutine 调用。
- 任务 `panic` 会被 recover 并转为失败；`Run` 后再注册任务返回 `ErrSchedulerStarted`。

同步原语仅使用 `sync.Mutex`、`sync.Cond`、`sync.WaitGroup`：单协调者在锁内完成
全部状态流转，工作 goroutine 只通过条件变量回报完成，状态无竞争。

## API 速览

```go
s := dag.New()
s.Add("a", fnA)
s.Add("b", fnB, "a")          // b 依赖 a；依赖 id 允许前向引用
err := s.AddDependency("b", "c") // b 额外依赖 c，受上述规则约束
report, err := s.Run(ctx)
report.StatusOf("b")             // dag.Succeeded / Failed / Blocked
report.Succeeded() / Failed() / Blocked()
```

## 目录

- `dag/dag.go` — 调度器实现
- `dag/dag_test.go` — `go test` 测试（含并发加边竞态用例，建议 `-race`）
- `cmd/demo/main.go` — 可运行样例：菱形、失败阻塞+独立分支、成环、动态加边

## 运行

```sh
go test -race -count=1 ./...
go run ./cmd/demo
```

### 样例输出

```
== 1. diamond: a -> b,c -> d (d runs exactly once) ==
  [000ms] a start
  [022ms] a ok
  [023ms] c start
  [023ms] b start
  [044ms] b ok
  [044ms] c ok
  [044ms] d start
  [056ms] d ok
  a   SUCCEEDED
  b   SUCCEEDED
  c   SUCCEEDED
  d   SUCCEEDED

== 2. failure blocks dependents, independent branch continues ==
  [056ms] solo start
  [056ms] bad start
  [068ms] bad FAIL: boom
  [091ms] solo ok
  bad FAILED (boom)
  child BLOCKED
  grand BLOCKED
  solo SUCCEEDED
  failed: [bad]  blocked: [child grand]  succeeded: [solo]

== 3. cycle detected before anything runs ==
  Run error: dag: cycle detected: a -> c -> b -> a

== 4. dependency added dynamically while Run is executing ==
  [091ms] root start; adding edge b -> a
  [091ms] a start (will be b's dynamically added dep)
  [114ms] root ok
  [132ms] a ok
  [132ms] b start
  [144ms] b ok
```

场景 4 中 `b` 原有依赖只有 `root`（114ms 已完成），动态加入的 `b -> a` 使其
等到 132ms `a` 完成后才启动。

### 测试结果

`go test -race -count=1 ./...` 全部通过（Go 1.24.2，darwin/arm64）：

- `TestDiamondRunsOnce` — 菱形末节点恰好运行一次，执行序合法
- `TestFailureBlocksDependentsNotOthers` — 失败只阻塞传递后继，独立任务成功
- `TestIndependentBranchesConcurrent` — 无依赖关系任务真正并发（两次 80ms 睡眠 < 140ms）
- `TestWaitsForAllDependencies` — 所有前驱成功后才启动
- `TestCycleDetectedBeforeRun` — 成环 0 任务运行即返回节点链，不挂起
- `TestSelfLoopIsCycle` — 自环视为环
- `TestDynamicDependencyHonoredAtRuntime` — 运行期加边对未启动任务生效
- `TestDynamicDependencyRejectedAfterStart` — 对已启动任务加边返回 `ErrTaskNotPending`
- `TestDynamicCycleRejectedAtRuntime` — 运行期加边成环被拒且调度器不挂起
- `TestUnknownNodeAndMissingDep` — 未知节点 / 缺依赖错误
- `TestMissingDependencyReleasesLock` — 校验失败后锁释放、可继续修改
- `TestDuplicateTaskAndEdge` — 重复任务与重复幂等边
- `TestPanicBecomesFailure` — panic 转失败并阻塞后继
- `TestAddAfterRunRejected` — Run 后禁止注册
- `TestConcurrentDynamicEdges` — 并发加边/完成流量（竞态检测）
