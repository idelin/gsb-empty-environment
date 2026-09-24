# Versioned Cache

一个只使用 Go 标准库的写入型缓存示例。组件把“数据源写入”和“缓存发布”分离，并通过单调版本号保证并发写按版本线性提交。

## 保证的语义

- 数据源写成功后才发布缓存值；数据源失败时返回错误并保留旧缓存。
- 缓存发布失败不会否认已成功的数据源写入，结果标记为 `partial`，缓存仍保留旧值。
- 每个写入获得单调递增版本；即使新版本的数据源先完成，旧版本未处理完时新版本也不会对读取可见，最终缓存不会被慢旧写回退。
- `Read` 只读取已发布的缓存值，不会观察到执行中的未提交内容。
- 相同非空 `RetryID` 且值相同的并发或后续请求共享同一次数据源写入结果；同 ID 不同值返回 `ErrRetryConflict`。
- `DataSource`、`Publish` 和 `Now` 都是可注入函数；默认不依赖远程缓存或网络。
- `Close` 取消仍在执行的数据源操作并等待内部 goroutine 退出。

## 使用方式

```go
store, err := versionedcache.New(versionedcache.Options[string]{
    DataSource: func(ctx context.Context, req versionedcache.SourceRequest[string]) error {
        // 向真实数据源执行幂等写入；req.RetryID 可用于下游去重。
        return nil
    },
    Publish: func(ctx context.Context, entry versionedcache.Entry[string]) error {
        // 本地缓存发布钩子；返回错误即可模拟或处理缓存失败。
        return nil
    },
    Now: time.Now,
})
if err != nil {
    panic(err)
}
defer store.Close()

result, err := store.Write(context.Background(), "value", "stable-retry-id")
if errors.Is(err, versionedcache.ErrCachePublish) && result.Status == versionedcache.StatusPartial {
    // 数据源已成功，但缓存未发布；后续同 RetryID 请求会得到同一个 partial 结果。
}

entry, ok, err := store.Read(context.Background())
```

## 测试

测试覆盖成功发布、数据源失败、缓存发布失败、读取隔离、版本反转、重试幂等、冲突重试 ID 和关闭释放资源。

```sh
go test ./...
go test -race ./...
go vet ./...
```

## 本次运行结果

```text
$ go test ./...
ok  	versionedcache	0.348s

$ go test -race -count=20 ./...
ok  	versionedcache	1.405s

$ go vet ./...
# 无输出，表示通过
```

`ExampleStore` 是可执行样例，可通过以下命令查看：

```sh
go test -run ExampleStore -v ./...
```
