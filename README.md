# 去标识化碰撞隔离

脱敏服务将标识符截短后出现碰撞，若直接合并会串接两个人的事件。写供Go服务调用的伪名分配包，短码仅作索引，内部以完整HMAC确认身份，碰撞时扩展长度但不能改变已经发布的短码；用显式别名关系保持旧事件可查询。并发分配原子完成，用故意碰撞的摘要替身验证不误合并和重启恢复。

## `pseudonym` 包

实现位于 `pseudonym/`，导入路径 `example.org/netsec/q0448/pseudonym`。

核心约定：

- 短码只是索引：完整 HMAC-SHA256 摘要的无填充小写 base32 前缀，不单独用于判定身份。
- 完整 HMAC 确认身份：`Confirm(code, digest)` 以恒定时间比较完整摘要，短码命中但摘要不符即拒绝。
- 碰撞只扩展不回改：新身份遇到已占用前缀就加长，直到找到空闲前缀；已发布短码永不改变，重复分配幂等。
- 显式别名：碰撞身份在最初发布的最短码下登记 `collision_extension` 别名；历史数据可用 `AddAlias` 登记 `legacy_migration`，`Resolve` 返回当前持有者与全部别名身份。
- 原子并发：`Allocate` 在互斥区内完成选码、落盘、索引更新与别名写入。
- 重启恢复：`WALStore` 以 JSON Lines 追加并 fsync，重启回放；末尾缺换行的半行按未提交丢弃，已提交区域损坏返回 `ErrCorrupt`。

快速使用：

```go
store, _, _, _ := pseudonym.OpenWAL("/var/lib/netsec/pseudo.wal")
defer store.Close()

alloc, _ := pseudonym.New(store, pseudonym.NewHMACDigester(hmacKey))
code, _ := alloc.Allocate(identity)        // 发布的短码，碰撞时自动加长
digest, _ := alloc.Digest(identity)        // 完整 HMAC，随事件一起保存

owners, _ := alloc.Resolve(storedCode)     // 含当前持有者与碰撞/历史别名
if alloc.Confirm(storedCode, eventDigest) { // 用完整摘要确认，防止误并
    // 事件确实属于该身份
}
```

测试使用“故意碰撞的摘要替身”（脚本化 Digester）构造共享前缀，覆盖不误合并、
并发原子性、WAL 截断/损坏与重启恢复：`go test ./... -race`。
