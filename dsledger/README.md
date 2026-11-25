# dsledger：签名份额防双签账本

面向 Go 共识宿主的可导入包（module `prescreen.local/ps-7934926941`，包路径
`prescreen.local/ps-7934926941/dsledger`），用于在阈值签名（如 threshold Ed25519）
场景下约束单个成员的份额签发行为，并在其双签时产出可独立验证的最小证据。

## 数据模型

- `Roster`：固定成员清单，成员有唯一 `MemberID` 与 Ed25519 公钥；绑定链 ID 与
  创世摘要，落盘后任何篡改都会导致 `Open` 失败。
- `Share`：份额消息。被签名载荷 `SigningBytes` 覆盖链 ID、高度、轮次、阶段
  （`prepare`/`commit`）、区块摘要、父摘要；`Signer`/`Sig` 不参与载荷，
  签名身份由验签公钥确定。规范编码为版本化域分隔符 + 长度前缀的定长布局，无歧义。
- `Evidence`：两份同一成员的有效份额 + 冲突类别，`VerifyEvidence` 仅靠成员公钥
  即可独立验证，不需要私钥或账本内部状态。

## 单成员安全规则

1. 同高度同阶段只能支持一个区块；第二个不同区块产出
   `same_height_block_conflict` 并冻结。
2. 同高度不同阶段（prepare 与 commit）不互相构成双签。
3. commit 必须已有同高度同区块、轮次不高于 commit 的 prepare；违反则产出
   `round_conflict` 并冻结。同槽位轮次只进不退：同区块更高轮次仅推进见证轮次，
   份额本体永不被后到消息覆盖，旧轮次判 `stale` 拒绝。
4. 高度 h 的 prepare，父摘要必须等于该成员“最近一次已 commit 高度（取最大高度）”
   的区块摘要；尚无 commit 时必须等于创世摘要。允许中间高度缺席。
5. 父摘要指向本成员已 commit 链上的旧块（非最新 tip）即跨高度父摘要分叉，产出
   `parent_digest_fork` 并冻结；父块尚未观察到时只暂存，绝不提前定罪。

## 乱序、暂存与冻结

- 前置缺失的份额进入按成员维护的暂存区；任意新份额落盘后，内部泵循环重新评估
  暂存，前置补齐即发布（结果 `OutcomeReleased` 并回填 `Released`）。
- 重复消息幂等（`OutcomeDuplicate`），不改变状态；冲突即冻结，冻结后该成员的
  一切新份额都被拒绝，不能用后到消息覆盖或“洗白”。
- 冻结成员的旧暂存不得继续推进后续高度：必须 `PurgeStaged` 清空暂存矛盾后，
  才能 `Unfreeze`；历史证据通过 `FrozenEvidence` 永久保留。

## 持久化与崩溃安全

- 每个成员一条仅追加 WAL：`member-<id>.wal`，帧为 `uint64 长度 + uint32 CRC32 + JSON`，
  每次状态变更先写帧并 `fsync`，再更新内存。
- 重启时重放 WAL 重建已签索引、暂存、tip 与冻结证据；尾部撕裂/CRC 损坏帧被
  截断丢弃，此前已确认事件（含冻结）全部保留，不漏报。
- 成员清单用临时文件 + fsync + rename 原子写入。

## 宿主最小用法

```go
roster, _ := dsledger.NewRoster(chainID, genesis, members)
ledger, _ := dsledger.Open(dataDir, roster)
defer ledger.Close()

share, _ := dsledger.SignShare(priv, chainID, id, height, round, dsledger.PhasePrepare, block, parent)
res, err := ledger.Ingest(share)
switch res.Outcome {
case dsledger.OutcomeAccepted, dsledger.OutcomeReleased:
    // 可向共识层发布
case dsledger.OutcomeStaged:
    // 前置缺失，已暂存，补齐后自动发布
case dsledger.OutcomeFrozen:
    _ = dsledger.VerifyEvidence(roster, *res.Evidence) // 独立举证
}
```

测试使用真实 Ed25519 密钥覆盖乱序 prepare/commit、同高度多轮、跨高度错误父项、
两份冲突份额、证据不可伪造、普通分叉不定罪、重启/撕裂帧/批次冻结等场景，
`go test ./... -race` 通过。
