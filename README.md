# 追踪数据脱敏与关联保留

`example.org/netsec/q0112` 提供供 Go 追踪宿主调用的伪名组件。它使用
HMAC-SHA256 为安全事件生成稳定标识，并在批次换代时原子更新事件键、引用和
映射表。

## 安全属性

- 伪名域分隔编码使用长度前缀绑定租户、字段类型、密钥代次和原始值，避免
  拼接歧义。同一租户、字段、代次和值跨服务稳定；不同租户的同一邮箱得到
  不同标识。
- 令牌格式为 `p1.<generation>.<base64url(HMAC-SHA256)>`，租户和字段类型
  不会以明文出现在令牌中。
- 映射表只保存 `旧令牌 -> 新令牌`。原始秘密只在 `Rotate` 调用期间用于
  内存中的 HMAC 计算，不进入映射、结果、令牌或错误信息。
- 一轮换代在验证全部主体、事件键和引用后才调用一次 `Publish`。存储失败时
  旧快照保持可见，不会泄露半批映射。
- 引用无法解析到目标代次时不会丢弃，而是以 `Reference{Missing: true}` 明确
  保留；映射链中的环也会以缺失结束，不会无限递归。

## 基本用法

```go
keys := map[netsec.Generation][]byte{
    1: oldKey, // 每个密钥至少 32 字节
    2: newKey,
}
ring, err := netsec.NewKeyring(1, keys)
p, err := netsec.NewPseudonymizer(ring)

token, err := p.PseudonymizeCurrent("tenant-a", "email", "alice@example.com")
```

换代时，主体列表提供重新计算新令牌所需的原始值，事件列表提供需要同步重写的
键和引用：

```go
result, err := p.Rotate(ctx, store, netsec.Batch{
    From: 1,
    To:   2,
    Subjects: []netsec.Subject{
        {Tenant: "tenant-a", FieldType: "email", Value: "alice@example.com"},
    },
    Events: []netsec.Event{
        {ID: "evt-1", Key: oldToken, References: []netsec.Token{otherOldToken}},
    },
})
```

`netsec.MappingStore` 的 `Publish` 必须原子替换快照。包内 `MemoryStore` 提供
并发安全的内存实现；持久化实现可对接数据库事务。

## 验证

```sh
go test ./...
go test -race ./...
go vet ./...
```

测试覆盖跨租户同值隔离、跨服务稳定性、事件键与循环引用同步改写、跨代映射链、
损坏映射环、整批校验失败不发布、存储中途失败回退，以及映射表拒绝原始秘密。
