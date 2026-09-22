# 确定性一致性反例缩减

这个仓库用 Go 标准库实现一个单进程、确定性的分布式 KV 事件模拟器，并对能触发一致性错误的事件序列做增量调试缩减。没有 goroutine、网络 I/O、真实时钟或第三方依赖；消息投递、故障和逻辑时间都由事件列表控制。

## 模型

- 两个副本 `A`、`B`，本地保存 KV 状态。
- 事件类型：`write`、`read`、`send`、`deliver`、`drop`、`clock`、`crash`、`recover`。
- 写在接收副本本地立即确认；复制消息只有在 `deliver` 时更新目标副本。
- `drop` 表示必要故障，后续同一消息的 `deliver` 作为调度步骤仍可重放，但会被标记为 `suppressed`。
- 时间只能由 `clock` 事件推进；读写等事件发生在当前逻辑时间。
- 读带 `expected` 时，若本地值不同，产生 `stale_read` 安全错误。
- 未完成操作到达 `deadline` 时，只产生 `deadline_exceeded` 活性错误。

## 缩减判定

`MinimizeCounterexample` 使用 ddmin 风格的分组删除：

1. 初始轨迹必须可重放，并包含恰好一个安全指纹。
2. 候选只有复现同一安全指纹、且没有活性错误时才接受。
3. 删除事件导致前置条件破坏时返回 `dependency_invalid`，不会当作成功缩减。
4. 只产生超时的候选返回 `liveness_timeout`，不会归类为安全失败。
5. 安全指纹变化返回 `different_safety_failure`。
6. 分组缩减结束后，对最终序列逐个删除事件做必要性审计。

关键依赖规则：

- `send` 必须引用已确认的 `write`，且负载一致。
- `deliver`/`drop` 必须引用已有 `send`。
- 每个 `drop` 都必须保留其计划中的 `deliver` 步骤；否则是在把显式故障偷偷改成“未投递”，候选拒绝。
- 跨副本读取已确认值时，必须保留对应复制路径；路径只能是实际投递，或带计划投递步骤的显式丢包。
- 删除必要 `drop` 后，消息正常送达，安全错误消失；该 `drop` 通过“去掉后不再失败”被证明必要。

## 运行

普通环境：

```sh
go test -v ./...
go run .
go run . -json
```

当前沙箱不允许写默认 Go 缓存时，使用临时缓存：

```sh
GOCACHE=/tmp/counterexample-reducer-gocache go test -v ./...
GOCACHE=/tmp/counterexample-reducer-gocache go run .
```

## 最小反例

内置 13 个事件的初始轨迹包含无关写入、复制、读取、崩溃和恢复。缩减后保留 5 个事件：

1. `w-x`：`A` 确认写 `k=1`。
2. `s-x`：`A` 向 `B` 发送复制消息 `m-x`。
3. `drop-x`：丢弃 `m-x`。
4. `deliver-x`：原计划投递被丢包抑制，状态为 `suppressed`。
5. `read-x-at-b`：`B` 读 `k`，预期 `1`，实际为空，触发 `stale_read`。

安全证据：

```text
safety: stale_read op=op-read-x node=B key=k expected="1" got="" at=0
B.k=""，A.k="1"；m-x={m-x A B op-write-x k 1 suppressed true false}
```

活性对照证据：

```text
liveness: deadline_exceeded op=op-pending-read node=B key=k expected="" got="" at=10
```

## 文件

- `simulator.go`：确定性重放、事件依赖校验、安全和活性判定。
- `minimizer.go`：候选分类、ddmin 分组缩减、单事件必要性审计。
- `sample.go`：安全失败和超时失败的内置可复现轨迹。
- `main.go`：文本和 JSON 证据输出。
- `simulator_test.go`：全部 `go test` 测试。
- `run_sample.txt`：一次真实文本运行输出，包含完整缩减过程。
- `test_results.txt`：一次真实 `go test -v` 结果。
