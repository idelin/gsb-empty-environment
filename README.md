# 小子群握手对静态秘密的累积泄漏

设备复用静态DH指数，又没检查对端元素阶，多个握手可能逐步暴露指数。请交付供Go安全实验宿主调用的恢复包，只用本地回调：秘密整数d满足0≤d<B，B≤2^32；每个实验给不大于2^31的素数p、非零元素g和精确阶q≤4096，回调返回SHA-256(g^d mod p的固定宽度大端字节||挑战字节)，宽度为p的字节长度。验证g阶恰为q，枚举0至q-1匹配回调摘要得到d的同余信息，再用广义中国剩余定理合并；q不要求互素，同余矛盾必须指出冲突实验，不能直接相乘模数。返回与全部观测相符且小于B的等差候选集合，只有一个候选时才称恢复成功。零个或多个摘要匹配分别明确报告，不能假定哈希无碰撞而悄悄取首个。提供拒绝非宿主批准大阶元素的修复回调，拒绝不产生同余。测试非互素兼容和冲突阶、B边界、伪造阶及多次握手才唯一的本地密钥，保留全部挑战和推导用于重放。

## 包结构

- `recovery.go`：阶校验、摘要枚举、广义 CRT 合并、会话与重放、脆弱/修复回调。
- `recovery_test.go`：非互素兼容、冲突阶、B 边界、伪造阶、多握手唯一化、碰撞/零匹配、重放与修复回调测试。

## 核心语义

- `ExactOrder` 用 `g^q=1` 且对 q 的每个素因子 r 满足 `g^(q/r)!=1` 验证阶恰为 q，伪造阶直接记为 `StatusBadOrder`，不产生同余。
- `Session.Observe` 枚举全部 `0..q-1`，零匹配记 `StatusNoMatch`，多匹配记 `StatusCollision` 并保留所有匹配残差，绝不静默取首个。
- `mergeCRT` 走广义 CRT：先检查 `(r2-r1)` 是否被 `gcd(m1,m2)` 整除，矛盾时由 `Result.Conflicts` 指出冲突实验 ID 与两条同余，模数不直接相乘。
- `Result.Progressions` 给出与全部观测相符且小于 B 的等差候选序列（首项、步长、项数）；候选数超过 `MaterializeLimit` 时只报告序列并置 `Truncated`，仅当物化结果恰有一个候选时 `Success` 才为真。
- 全部观测（含挑战、宽度、摘要、枚举残差、合并步骤）保留在 `Session` 中，`Replay` 无需回调即可重新推导。
- `HardenedCallback` + `ApprovalSet` 是修复路径：白名单批准前强制精确阶校验，未批准元素返回 `ErrElementNotApproved`，会话记为 `StatusRejected`，拒绝不产生同余。

## 最小用法

```go
sess, _ := netsec.NewSession(big.NewInt(1<<32), netsec.VulnerableCallback(secretD))
for i, hp := range handshakes {
    obs := sess.Observe(i+1, hp)
    switch obs.Status {
    case netsec.StatusUnique, netsec.StatusCollision:
        // 可用同余（碰撞时保留多条分支）
    case netsec.StatusNoMatch, netsec.StatusBadOrder, netsec.StatusRejected:
        // 显式异常，不参与合并
    }
}
res := sess.Derive()
if res.Success {
    // res.UniqueSecret 即唯一候选
}
replayed, _ := netsec.Replay(B, sess.Observations(), 0)
```
