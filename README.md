# OSPF自生成LSA反击准入

攻击者向OSPFv2区域注入声称由本路由器生成的LSA，或用MaxAge清洗他人的LSA。给Go路由进程写供LSA接收调用的数据库准入包：先按加密认证序号检查邻居报文，序号倒退拒绝；收到自己Router ID生成且序号不低于本机实例的LSA时，用更高序号重新生成而不接受，反击频率受限；LS序号按RFC 2328回绕比较；数据库中没有对应实例的MaxAge LSA直接确认并丢弃，不新建条目。用伪造自生成LSA、序号回绕、孤立MaxAge和合法洪泛验证数据库。

## 实现

准入逻辑位于 `ospf/lsadb`，供路由进程在收到 LSA Update 时调用 `Database.Admit`：

- `auth.go`：按邻居维护加密认证序号（RFC 2328 附录 D.1 / RFC 5709）。序号倒退按重放拒绝，相等视为可接受的重复且不推进记录。
- `sequence.go`：LS 序号按 RFC 2328 §12.1.2 的 32 位循环序号空间比较（`CompareSequence`），`NextSequence` 在到达 `MaxSequenceNumber` 时返回初始序号并要求先做 MaxAge 冲刷（§12.1.4）。
- `db.go`：`Admit` 依次执行认证序号检查 → 校验和/长度/保留序号校验 → MaxAge 处理 → 自生成 LSA 反击 → 普通洪泛比较（§13）。
- 自生成 LSA 永不入库：收到序号不低于本机实例的自生成 LSA 时，以严格更高序号重新生成并洪泛；同一 LSA key 的反击按 `DefenseConfig.ReoriginateInterval` 限速，限速期间返回 `OutcomeSelfDefenseThrottled` 且库不变。
- 孤立 MaxAge（库中无对应实例）只确认不建条目；命中实例的 MaxAge 删除该实例并把 MaxAge LSA 洪泛出去。
- `checksum.go`/`wire.go`：Fletcher-16 LS 校验和（LS age 与 checksum 字段不参与）及 LSA 编解码。

`Decision` 通过 `Outcome` 告诉调用方是否需要 `Ack`/`Flood`/替换或删除库条目，`Stats` 提供累计计数。

## 运行

```sh
go test -race ./...
go test -cover ./...
```

验证场景对应测试：

- 伪造自生成 LSA：`TestForgedSelfLSA`、`TestForgedSelfEqualSequence`、`TestForgedSelfWithoutLocalInstance`、`TestSelfDefenseThrottle`
- 序号回绕：`TestCompareSequence`、`TestSelfDefenseWrapsSequence`、`TestForeignLSASequenceWrapAccept`
- 孤立 MaxAge：`TestOrphanMaxAgeAckedAndDiscarded`、`TestMaxAgeFlushesExistingInstance`
- 合法洪泛：`TestLegalFloodAcceptsNewLSA`、`TestLegalFloodReplacesOlder`、`TestLegalFloodDuplicate`、`TestLegalFloodOlderRepliesWithCurrent`
- 认证序号倒退：`TestAuthTrackerMonotonic`、`TestAdmitRejectsAuthRegression`
