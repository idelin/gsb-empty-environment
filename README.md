# suspense — 响应式异步资源加载与 Suspense 状态机

纯 Go 标准库实现：将响应式信号（`Signal`）与异步 Promise（`Fetcher`）绑定，
以下游可订阅的状态机（`Pending / Success / Error`）呈现异步请求生命周期，
并在并发请求竞争时自动作废旧请求的结果。

## 核心概念

- `Signal[T]`：响应式数据源（如查询参数），`Set` 时同步通知订阅者。
- `Fetcher[P, R]`：异步请求函数，签名 `func(ctx, P) (R, error)`，需响应 `ctx` 取消。
- `Resource[P, R]`：绑定 Signal 与 Fetcher 的状态机。
  - 每次 `Set` 触发新一代（epoch+1）请求：作废旧请求的 `context`，
    同步向下游传播 `Pending` 信标，然后异步执行 fetch。
  - fetch 完成时校验 epoch：若已被更新的请求取代（stale promise），直接丢弃结果。
  - 通知经序列化，下游观察到的状态迁移严格有序。
- `Snapshot[R]`：不可变状态视图 `{State, Value, Err, Epoch}`。

## 状态机

```
Set(query) ──▶ Pending ──▶ Success   (fetch 成功且 epoch 最新)
                  │
                  └──────▶ Error     (fetch 失败且 epoch 最新)

新的 Set() 使旧 epoch 的在途/迟到结果全部作废（discarded）。
```

## 运行

```sh
go test -race -v ./...   # 测试（含验收竞态场景）
go run ./cmd/demo        # 运行样例：搜索即输即搜，慢请求被作废
```

## 验收场景

`TestRaceDiscardsStalePromise`：快速连续设置查询 A、B 发起两次异步获取；
A 的旧 Promise 被刻意延迟且忽略取消。系统检测到第二次请求后自动作废 A，
下游从未观察到 A 的结果，最终稳定呈现 B 的 `Success`。
