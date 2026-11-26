# 防双签审计与惩罚定罪组件设计

包路径：`doublesign`（模块 `formal.local/w3-39e1170702`）。仅依赖 Go 标准库，
使用真实 `crypto/ed25519` 签名，可直接被共识宿主导入。

## 1. 纪元验证人集与签名根锚定

- `EpochSet` 绑定 `epoch`、`chainID` 与固定成员列表；每个 `Member` 含
  不可变 `ID`、Ed25519 公钥与投票权重。
- 跨纪元通过注册独立的 `EpochSet` 实现：成员权重可变更，成员可被剔除。
- `Root(EpochSet)` 对成员 ID、公钥、权重与纪元做确定性长度前缀二进制编码后
  SHA-256；`AuthorityAnchor` 用治理权威私钥对该根签名。
- 离线验证时，证据内嵌整套 `EpochSet` 与锚定签名，仅凭权威公钥即可确认
  验证人集本身合法，无需宿主数据库。

## 2. 签名消息覆盖范围

`SigningBytes` 对以下字段做域分隔（`BFT-AUDIT/V1/SIGN`）的确定性编码并签名：

`chainID, epoch, height, round, phase, blockDigest, parentDigest, lockedRound,
qcHeight, qcRound`。

- `phase ∈ {prepare, commit, view_change}`。
- `view_change` 额外携带其宣称的最高 prepareQC 的 `qcHeight/qcRound`，同样
  进入签名负载，因此伪造或篡改 QC 声明必然导致验签失败或谓词失败。
- 跨纪元交界消息必须显式带 `epoch`；未注册纪元或已剔除成员的份额直接拒绝
  （`rejected`，不定罪），旧纪元合法份额重放为幂等 `duplicate`。

## 3. 安全规则（按成员独立状态机）

1. 同轮双签（`equivocation`）：同 `(epoch,height,round,phase)` 出现不同
   `blockDigest` 即定罪。
2. commit 前置 prepare（`commit_without_prepare`）：commit 要求同高度同区块、
   `round <= commit.round` 的本成员 prepare；若只存在冲突 prepare 立即定罪，
   否则进入乱序暂存等待。
3. LockedQC 不可回退（`locked_qc_rollback`）：一旦在某高度确认更高
   `lockedRound`，在更高 round 上退回支持低锁定的冲突区块即定罪。
4. parent 精准衔接（`parent_link_mismatch`）：prepare 的 `parentDigest` 必须
   等于该成员最近已 commit 最高高度区块的 digest，且高度严格 +1；不满足则
   暂存（依赖未到）或定罪（明确冲突）。
5. view_change QC 约束（`view_qc_forged`）：宣称的最高 prepareQC 必须单调
   且由本成员对应高度/轮次、同区块的 prepare 支撑；回归或无支撑即定罪。

## 4. 乱序、冻结与崩溃恢复

- `Auditor.Observe` 先做结构、纪元、成员、签名校验，通过后进入状态机；
  依赖未满足的真实份额进入 `staged`，每次有新份额落地后 `pump` 重试。
- 定罪是不可变的：成员 `frozen=true`，后续所有份额返回 `frozen`，且该成员
  所有旧暂存立即清除，永不重新激活。
- `Store` 采用追加式 JSONL（`epochs.jsonl`、`messages.jsonl`）并在每次追加
  后 `fsync`；定罪证据以原子 rename 写入 `evidence/`。
- 重开目录时 `WithStore` 先注册纪元再按原始到达顺序无追加重放，重建活动
  状态、暂存与冻结；证据目录作为冻结状态的兜底来源，保证崩溃不漏报。

## 5. 最小离线定罪证据

`Evidence` 仅包含：链 ID、违规定型、被定罪成员、内嵌纪元集及其权威锚定、
支撑违规的最少签名消息（双签为两条，其它为一至两条）。

`VerifyEvidence(ev, authorityPub)` 完全离线执行：

1. 校验纪元集结构与权威锚定签名；
2. 确认被告是该纪元成员，并对每条消息用成员公钥做 Ed25519 验签；
3. 执行与违规标签匹配的密码学谓词，标签与证据不符即拒绝。

由于所有安全字段都在签名负载内，且私钥从不离开成员，任何只持有公钥的一方
都无法构造可通过验证的定罪证据（见 `forgery_test.go`）。

## 6. 独立 CLI

`cmd/doublesign-verify` 读取证据 JSON 与权威公钥（hex）即可独立验签：

```
go run ./cmd/doublesign-verify -authority-key <hex> conviction.json
```

## 7. 测试覆盖

`go test -race ./...` 覆盖：诚实流、乱序暂存与自动激活、五类定罪、跨纪元
权重变更与剔除重放、未知纪元拒绝、并发观察、崩溃恢复不漏报、冻结后旧暂存
不激活，以及仅持公钥无法伪造证据（含 CLI 端到端验签）。
