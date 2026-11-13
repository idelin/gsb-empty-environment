# Password-reset session revocation for Go auth hosts

本包实现密码重置提交路径，目标是消除“重置成功后旧刷新令牌或并行登录事务仍能签发新会话”的窗口。

## 安全不变量

一次成功的 `CommitReset` 在同一个线性化点内完成：

- 校验账户仍处于事务开始时的 `CredentialEpoch` 和 64 位 `SessionEpoch`。
- 写入隔离区预先算好的新密码哈希。
- `CredentialEpoch` 与 `SessionEpoch` 各加 1；任一达到 `math.MaxUint64` 时拒绝提交并触发告警，绝不回绕。
- 消费 `ResetID`，写入提交回执。
- 撤销该账户的全部刷新令牌族。
- 取消该账户其他所有待处理登录事务和重置事务。

任何校验失败都不会改动密码、epoch、令牌族或事务状态。提交回执与状态变更原子落库；相同 `ResetID` 和相同目标密码的重试只返回原回执，不会再次加 epoch。

## 使用方式

```go
signer, err := netsec.NewTokenSigner(tokenKey) // 至少 32 字节，跨重启保持稳定
if err != nil {
    return err
}

host, err := netsec.NewService(netsec.Config{
    Verifier: verifyRecoveryProof, // 调用方验证证明，返回已满足的方法集合
    Signer:   signer,
    OnAlarm:  raiseSecurityAlarm,
})
if err != nil {
    return err
}

if err := host.CreateAccount(ctx, "alice", initialPassword,
    host.DefaultParams(),
    netsec.RecoveryPolicy{Required: []netsec.RecoveryMethod{
        netsec.RecoveryMethodEmail,
    }},
); err != nil {
    return err
}

// 1. 验证恢复证明并绑定开始 epoch、当前策略摘要、目标密码参数和截止时刻。
txn, err := host.BeginReset(ctx, netsec.BeginResetRequest{
    AccountID:            "alice",
    TargetPasswordParams: host.DefaultParams(),
    Proof:                proof,
    Deadline:             time.Now().Add(10 * time.Minute),
})
if err != nil {
    return err
}

// 2. 在锁外完成昂贵的密码哈希。
prepared, err := host.PreparePassword(ctx, newPassword, host.DefaultParams())
if err != nil {
    return err
}

// 3. 单一事务提交；失败时零生效。
receipt, err := host.CommitReset(ctx, txn, prepared)

// 响应丢失且恢复证明已被消费时，可按 resetID 取回原回执。
storedReceipt, ok := host.LookupReceipt("reset-id")
```

访问令牌与刷新令牌：

```go
claims, err := host.VerifyAccessToken(ctx, accessToken) // 每次比较完整 uint64 sessionEpoch
pair, err := host.RefreshTokens(ctx, refreshToken)      // 令牌族代次单次轮换
```

## 关键点

- `BeginReset` 在调用恢复验证回调之前捕获 epoch。验证期间若有另一笔重置提交，本事务在提交时会因 epoch 变化或已被取消而失败。
- `CommitReset` 不信任事务开始时的策略摘要，而是按账户当前的 `RecoveryPolicyDigest` 重新解析策略并检查证明方法集合。策略收紧会立即影响待提交事务。
- `PreparePassword` 不持有存储锁，返回不可伪造的 `PreparedPassword`；其参数在准备阶段和提交阶段都通过下限检查。
- 访问令牌和刷新令牌都携带 `CredentialEpoch`、`SessionEpoch`。刷新令牌额外绑定 `FamilyID` 和 `Generation`；同一代令牌只能成功刷新一次。
- 提交回执中的目标密码指纹使用服务端 HMAC 密钥识别同一密码的重试，不保存明文密码。

## 持久化与密钥

`MemoryStore` 是线性化参考实现。生产宿主需要把同样的原子提交语义放入数据库事务或条件写中：账户 epoch 比较、密码哈希、回执、令牌族撤销、登录/重置事务取消必须一起提交。

以下密钥/状态必须跨重启保持稳定：

- `TokenSigner` 的签名密钥。
- `PasswordFingerprintKey`（或 `MemoryStore` 中持久化的指纹密钥），否则无法识别同一密码的回执重试。
- 已提交的 `ResetID` 回执和 epoch 值。

默认 `PBKDF2Hasher` 使用标准库实现。宿主可通过 `PasswordHasher` 接口替换为 Argon2id、HSM 或 KMS 隔离哈希服务。

## 验证

```sh
go test ./...
go test -race ./...
go test -run TestDeterministicInterleavings -v
```

确定性模型测试对“旧令牌刷新、旧登录完成、重置 A 提交、重置 B 提交”的所有线性化顺序做 BFS，返回最短的“重置后仍用旧证明/旧令牌签发会话”轨迹；当前实现应返回空轨迹。另一个测试用故意忽略 epoch 的抽象实现验证搜索器能在两步内找到违规轨迹。
