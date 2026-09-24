# coalesce

失效合并 + 限速刷新的 Go 库（仅标准库，无远程缓存依赖）。

## 特性

- **合并失效**：同一键的失效风暴合并为一次刷新（千次失效 ≠ 千次加载）
- **限速刷新**：可注入时钟的间隔限速器，一次只刷新一个键
- **关键键优先**：`IsCritical` 标记的键插队刷新
- **版本水位**：每键 `dirtyVersion` 水印只保留最高代际，合并不丢最新失效
- **队满保守标失效**：溢出键立即不可读，绝不用旧值冒充有效，腾出容量后补刷新
- **代际精确确认**：刷新成功只确认其加载时实际覆盖的代际；加载期间到达的新失效保持脏并重新调度
- **可注入**：数据源 `Load`、时钟 `Now/After`、失败回调 `OnError` 均为函数注入
- **资源释放**：`Close()` 停止后台 worker 并等待其退出

## 用法

```go
iv := coalesce.New(coalesce.Options{
    Load:       myDB.Load,                       // 数据源
    IsCritical: func(k string) bool { ... },     // 关键键
    OnError:    func(k string, err error) { },   // 失败回调
    Capacity:   1024,                            // 队列容量
    Interval:   50 * time.Millisecond,           // 刷新限速
})
defer iv.Close()

iv.Invalidate("user:42", version) // 合并、排队、限速刷新
v, ok := iv.Get("user:42")        // 脏/溢出期间 ok=false
```

## 运行

```sh
go test ./...    # 测试
go run ./cmd/demo # 演示
```
