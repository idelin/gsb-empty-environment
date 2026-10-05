# migflight

Single-flight 请求管理库：运行中的任务可以把临时键升级为正式键；
正式键已有任务时，等待者合并到已有任务上。

## 用法

```go
g := migflight.NewGroup()

res := g.Do("tmp-123", func(h *migflight.Handle) (any, error) {
    id := resolveFormalID()          // 任务运行中得知正式键
    h.Promote("user:" + id)          // 迁移到正式键
    return load("user:" + id)
})
```

## 迁移协议

- `Handle.Promote(formal)` 返回 `false`：正式键空闲，当前调用改挂到正式键下
  继续执行，之后对正式键的请求加入它。
- 返回 `true`：正式键已有在途调用，本调用的等待者改为等待正式键调用的结果；
  本调用自身的结果对等待者丢弃（执行函数仍从 `Do` 拿到自己的返回值）。
- 迁移会把旧键从注册表摘下并推进其代次（generation）：旧键的新请求开启
  明确的新代次，通过 `Handle.Gen()` / `Result.Gen` 可观察。
- 无死锁：全部共享状态由单把互斥锁保护，用户函数在锁外执行，等待者只
  阻塞在 channel 上；合并目标一定是在册（未被合并）的调用，合并链无环。
- `DoCtx` 的等待可取消：某个等待者取消只影响自己，不影响在途调用和其他
  等待者。

## 测试

```sh
go test -race ./...
```

含两个临时键并发迁往同一正式键的验证（`TestConcurrentPromoteSameFormalKey`）：
恰好一方认领、另一方合并，双方等待者都拿到认领方的结果。
