# 扩展属性策略快照

对象内容与安全标签必须作为同一个版本一起发布。若把标签放在扩展属性中、
先写内容再写标签，读者可能在两个操作之间读到“新内容 + 旧标签”，从而被
错误放行。本包为 Go 对象服务提供带版本的发布模型：

- 内容摘要（SHA-256）和安全标签在同一个 `Publish` 事务中提交；
- `Get`/`Acquire` 返回不可变 `Handle`，其版本号、摘要、标签和内容来自同一次提交；
- 零值标签视为“缺标签”，发布和读取都默认拒绝；
- 新标签等级低于当前等级时，必须通过 `ApproveDowngrade(currentVersion)` 提供当前版本号，
  过期或错误的批准版本会被拒绝；
- `WithCommitHook` 提供持久化屏障与故障注入点，提交失败不会暴露新版本的任何字段。

```go
store := netsec.NewStore()

v1, err := store.Publish("service/config", content, netsec.Label{
    Name:  "secret",
    Level: 3,
})
if err != nil {
    return err
}

// 读者只拿到完整版本：摘要、标签和内容彼此绑定。
h, err := store.Acquire("service/config")
if err != nil {
    return err
}
if !h.Verify() {
    return errors.New("release digest mismatch")
}
_ = h.Digest()
_ = h.Label()
_ = h.Content()

// 降级必须显式批准当前版本。
_, err = store.Publish("service/config", next, netsec.Label{
    Name:  "public",
    Level: 0,
}, netsec.ApproveDowngrade(v1.Version()))
```

测试覆盖提交失败回滚、缺标签拒绝、过期降级批准，以及并发读写时每个句柄
都对应完整旧版或新版。
