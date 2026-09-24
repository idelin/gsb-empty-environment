# writecache

版本化写穿缓存协调器（仅 Go 标准库，无远程缓存依赖）。

## 语义

- 写先落数据源，成功才提交并发布缓存；数据源失败保留旧值并返回错误。
- 并发写按单调版本号线性化；慢旧写晚完成时被判 `ErrStaleWrite`，不覆盖新版本、不发布缓存。
- 读只看见已提交内容，在途写不可见。
- 缓存发布失败不否认数据源已成功：返回 `PartialError` 并标记 `StatusPartial`，读者可见已提交值。
- 写携带调用方 ID，同 ID 重试幂等：成功/失败均回放记录结果；部分状态的重试仅补发缓存，不重写数据源。
- 数据源、缓存、时钟、失败注入均为可注入函数（`Options`），测试全程进程内完成。
- `Close` 等待在途写结束并精确释放一次资源（`Closer`），关闭后写返回 `ErrClosed`。

## 运行

```sh
go test ./... -race -count=1 -v   # 测试
go run ./cmd/demo                 # 运行样例
```

## 结构

- `store/store.go` — 协调器实现
- `store/store_test.go` — 全部语义的 go test 用例
- `cmd/demo/main.go` — 端到端运行样例（内存数据源 + 内存缓存）
