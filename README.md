# 去标识化碰撞隔离

`netsec` 是一个无外部依赖的 Go 伪名分配包。它解决的是“短码碰撞后把两个
人的事件错误串接”的问题：短码只用于索引，身份始终由完整 HMAC 确认。

## 核心不变量

- 身份的键是 `HMAC-SHA-256(key, identifier)` 的完整摘要，原始标识符不落盘。
- 短码是完整摘要的 Base32 前缀，默认 12 个字符（60 bit）。
- 短码碰撞时不合并身份。已经发布并返回给调用方的短码永远不变；新身份从
  更长一位的前缀开始扫描，直到找到未被占用的规范短码。
- 新身份经过的每个较短前缀都作为显式别名写入快照。旧事件同时带有旧短码和
  标识符时，`Resolve` 会用完整 HMAC 找到当前规范短码。
- 只用短码查询时可能得到多个候选。`Candidates` 会如实返回它们，绝不按前缀
  合并；写入或关联事件前必须用 `Resolve` 确认身份。

## 使用

```go
store, err := netsec.NewFileStore("/var/lib/deid/pseudonyms.json")
if err != nil {
    return err
}

alloc, err := netsec.NewAllocator(netsec.Options{
    Key:               hmacKey, // 至少 16 字节，实际部署建议 32 字节随机值
    Store:             store,
    InitialCodeLength: netsec.DefaultInitialCodeLength,
})
if err != nil {
    return err
}

// 分配或取回稳定短码。
shortCode, err := alloc.Assign(ctx, identifier)

// 处理历史事件时，用事件里的旧短码 + 原始标识符确认身份。
// 若旧短码是别名，current 是当前规范短码；若属于别人则返回
// netsec.ErrIdentityMismatch，绝不会返回对方的短码。
current, err := alloc.Resolve(ctx, eventShortCode, identifier)

// 只有短码、尚不知道身份时，先查看索引是否歧义。
candidates, err := alloc.Candidates(ctx, eventShortCode)
if len(candidates) == 1 {
    // 索引唯一，但仍应在写入前用 Resolve 校验身份。
} else {
    // 需要事件携带的完整身份信息才能安全归属。
}
```

`Assign` 对同一标识符幂等；重复调用不会写盘。短码大小写敏感，调用方若要
统一大小写或 Unicode 形式，应在调用前完成规范化，包内不做隐式改写。

## 并发与恢复

- `Allocator` 用互斥锁把“查重、选码、更新别名、落盘”作为一个事务串行化。
  同一进程内并发分配不会得到重复规范短码，同一身份只会得到一个短码。
- 快照保存失败时内存状态回滚，不会留下只在内存中存在的“幽灵分配”。
- `FileStore` 先写同目录临时文件、`fsync`、原子 `rename`，再同步目录；快照
  权限为 `0600`。重启后从同一快照恢复短码和别名。
- 短码是精确索引键。发生碰撞后，旧短码会成为新短码的前缀（例如 `AB` 与
  `ABC` 并存），查询时不要做前缀匹配或范围扫描。
- 快照记录密钥检查值。换 key 或改变初始短码长度后打开旧快照会返回
  `ErrIncompatibleStore`，避免把已有人员静默当成新身份。
- `FileStore` 面向单写进程。多进程/多副本部署时应实现带事务或 CAS 语义的
  `Store`（例如数据库表），不要共享同一个 JSON 文件。

## 验证

```sh
go test -race ./...
```

测试通过可注入的 `Options.MAC` 构造“前四位摘要相同、完整摘要不同”的碰撞：
验证旧码不变、别名可回查、不同人员解析结果不同；并覆盖并发分配、保存失败
回滚、原子文件替换和重启恢复。
