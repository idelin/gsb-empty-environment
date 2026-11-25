// Package dsledger 实现面向 Go 共识宿主的、可导入的阈值签名份额防双签账本。
//
// 核心能力
//
//   - 固定成员清单（NewRoster）：每个成员有唯一 ID 与 Ed25519 公钥，清单落盘后不可变更。
//   - 份额消息（Share）：签名覆盖链 ID、高度、轮次、阶段、区块摘要、父摘要（SigningBytes）。
//   - 安全规则（单成员维度）：
//     同高度同阶段只能支持一个区块，否则产出 ConflictSameHeight 证据并冻结；
//     同高度不同阶段不构成双签；
//     commit 必须存在同高度同区块、轮次不高于 commit 的 prepare，否则产出
//     ConflictRound 证据并冻结；同槽位轮次只进不退，后到的旧轮次被拒绝且不覆盖；
//     prepare 的父摘要必须等于该成员最近一次已 commit 高度的区块摘要
//     （无 commit 时等于创世摘要），允许中间高度缺席；父项指向已 commit 链上
//     旧块即构成 ConflictParentFork。
//   - 乱序处理：前置缺失的份额进入按成员维护的暂存区，前置补齐后由内部泵循环发布。
//   - 可独立验证的最小证据（Evidence/VerifyEvidence）：仅需成员公钥，无需私钥或内部状态。
//   - 冻结治理：冻结后拒绝该成员一切新份额；必须 PurgeStaged 清理暂存矛盾后才能
//     Unfreeze，杜绝基于旧暂存继续推进后续高度。
//   - 持久化：每成员一条仅追加 WAL（长度前缀+CRC32 帧），状态变更先 fsync 落盘；
//     重启重放重建已签索引、暂存与冻结证据，尾部撕裂帧自动截断，重复消息幂等。
package dsledger
