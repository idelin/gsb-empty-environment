# gencache — 代际控制缓存（Go 标准库）

并发安全的泛型缓存，以「代际（generation）」控制写入，杜绝失效后旧加载复活旧值。

## 语义

- **代际记录**：每次加载开始时记录有效代际 = 全局代际 + 键代际。
- **失效**：`Invalidate(key)` 提升键代际；`Clear()` 提升全局代际并清空条目。
- **防复活**：加载完成时仅当代际仍有效才写入缓存；失效后的旧结果只返回给本次加载的等待者。
- **等待者语义**：同键同代的并发 `Get` 共享一次加载，等待者直接获得该次加载的值或错误（即使未入缓存）。
- **不互相阻塞**：不同键的加载完全独立；失效后新加载可在旧加载结束前启动。
- **可注入**：数据源 `Source`、时钟 `Now`、失败回调 `OnError`、TTL 均为注入项，无远程缓存依赖。

## 使用

```go
c := gencache.New(gencache.Options[string, string]{
    Source:  func(k string) (string, error) { return load(k) },
    Now:     time.Now,          // 可注入假时钟
    TTL:     time.Minute,       // <=0 不过期
    OnError: func(k string, err error) { log.Println(k, err) },
})
v, err := c.Get("key")
c.Invalidate("key")
c.Clear()
```

## 运行

```sh
go test -race -v ./...   # 测试
go run ./cmd/demo        # 运行样例
```
