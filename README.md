# scope — Go 结构化并发作用域

仅使用 Go 标准库（`sync` + `context`）实现的结构化并发原语：

- **子任务归属父作用域**：`Scope.Go` 启动的任务由父作用域持有，`Wait` 负责收口。
- **失败触发兄弟取消**：首个子任务失败立即取消作用域 context，兄弟任务通过 `ctx.Done()` 感知。
- **退出等待全部清理**：`Wait` 阻塞到所有子任务返回，包括取消后仍在清理的任务——父不会提前返回。
- **聚合错误保留原始原因**：`Wait` 返回 `*scope.Error`，`First` 是首个失败（原始原因），`All` 按到达顺序保留全部错误；兄弟任务上报的 `context.Canceled` 不会覆盖首个失败（取消发生在首个失败落账之后，构造上不可能覆盖）。
- **嵌套作用域**：子任务可用 `scope.New(ctx)` 派生子作用域，父 `Wait` 会等整棵子树完成，不泄漏 goroutine。
- **重复取消安全**：`context.CancelFunc` 幂等，并发/重复失败安全（`-race` 验证）。
- **测试注入**：作用域本身不含任何定时器，只有标准同步原语；任务函数、时序（channel + 显式超时）与失败均由测试注入。

## API

```go
s := scope.New(parentCtx)
s.Go(func(ctx context.Context) error { ... }) // 子任务
err := s.Wait()                                // 等待全部子任务，返回聚合错误
```

`err` 为 `*scope.Error`：`errors.Is/As` 可匹配 `First` 及 `All` 中的任意错误。

## 运行样例

`example_test.go` 中的 `Example` 随 `go test` 执行，输出：

```
worker: cleaned up after context canceled
wait: scope: disk full (and 1 more error(s))
cause is disk full: true
```

## 测试结果

`go test -race -v ./...`（Go 1.24，darwin/arm64）：

```
=== RUN   TestAllSucceed
--- PASS: TestAllSucceed (0.00s)
=== RUN   TestFailureCancelsSibling
--- PASS: TestFailureCancelsSibling (0.00s)
=== RUN   TestWaitBlocksUntilCleanupFinishes
--- PASS: TestWaitBlocksUntilCleanupFinishes (0.00s)
=== RUN   TestCancellationDoesNotOverrideFirstFailure
--- PASS: TestCancellationDoesNotOverrideFirstFailure (0.00s)
=== RUN   TestNestedScopes
--- PASS: TestNestedScopes (0.00s)
=== RUN   TestConcurrentFailuresRepeatedCancelSafe
--- PASS: TestConcurrentFailuresRepeatedCancelSafe (0.00s)
=== RUN   TestParentCancelPropagates
--- PASS: TestParentCancelPropagates (0.00s)
=== RUN   TestGoAfterWaitPanics
--- PASS: TestGoAfterWaitPanics (0.00s)
=== RUN   TestNoGoroutineLeak
--- PASS: TestNoGoroutineLeak (0.00s)
=== RUN   Example
--- PASS: Example (0.00s)
PASS
ok  	scope	1.408s
```

`go test -race -count=5 ./...` 重复 5 轮全部通过，`go vet` 无告警。

关键测试与需求的对应关系：

| 需求 | 测试 |
| --- | --- |
| 失败触发兄弟取消 | `TestFailureCancelsSibling` |
| 清理未完成父不返回 | `TestWaitBlocksUntilCleanupFinishes`（channel 判定，无 sleep） |
| 取消不覆盖首个失败 | `TestCancellationDoesNotOverrideFirstFailure` |
| 嵌套作用域 | `TestNestedScopes` |
| 重复取消安全 | `TestConcurrentFailuresRepeatedCancelSafe`、`TestParentCancelPropagates` |
| goroutine 不泄漏 | `TestNoGoroutineLeak` |
