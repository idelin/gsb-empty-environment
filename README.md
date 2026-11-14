# 跨链桥消息证明组件

`example.org/netsec/q0453` 提供可直接导入 Go 跨链桥的消息证明验证组件。它把源链锁仓事件绑定到源链、区块、交易、事件索引和目标链，并在验证共识签名、轻客户端头链、Merkle 根与最终性后，为目标链释放提供一次性授权。

## 安全目标

- **伪造事件**：事件叶包含源链、目标链、区块高度、交易哈希/索引、事件索引、事件类型、发送方、资产、金额、接收方与 nonce；任何字段被修改都无法通过签名头中的 `EventRoot`。
- **旧证明重放**：`Process` 以 `(source, destination, txHash, eventIndex)` 的唯一键原子消费消息，重复提交返回 `ErrAlreadyProcessed`。
- **跨链/跨桥重放**：目标链与可选 `BridgeID` 写入事件叶，并与 `Verifier` 配置、证明字段和幂等键绑定；换目标链或换桥实例会返回 `ErrDestinationMismatch`、`ErrBridgeMismatch` 或 `ErrForgedEvent`。
- **伪造共识/包含证明**：验证 >2/3 投票权、拒绝重复签名者，并分别验证交易叶到 `TxRoot`、事件叶到 `EventRoot` 的 Merkle 包含证明。
- **链重组/最终性**：默认仅允许已最终化区块中的事件释放资产；若新分支替换已最终化区块，轻客户端冻结并返回 `ErrFrozen`/`ErrFinalityViolation`，不会继续释放。
- **只释放已锁定资产**：每个已认证 lock 事件最多授权一次释放；金额与接收方不能被篡改。源链仍须在共识层保证 lock 事件对应真实托管。

## 快速使用

```go
lc, err := netsec.NewLightClient(netsec.TrustedState{
    Header:     checkpointHeader, // 带外获得的已信任检查点
    Validators: trustedValidatorSet,
}, 12) // 12 个区块后视为最终
if err != nil {
    return err
}

verifier, err := netsec.NewVerifier(netsec.Config{
    SourceChain:      "ethereum",
    DestinationChain: "chain-b",
    BridgeID:         "bridge-b-v1",       // 可选，绑定目标链部署实例
    LightClient:      lc,
    Policy:           netsec.DefaultPolicy(), // 仅 lock、必须最终化
    Store:            replayStore,            // 生产环境使用持久化 Store
})
if err != nil {
    return err
}

// 先提交并验证共识签名的头，再处理事件证明。
if err := verifier.AddHeader(header, headerSignatures); err != nil {
    return err
}

release, err := verifier.Process(ctx, messageProof)
switch {
case errors.Is(err, netsec.ErrAlreadyProcessed):
    // 幂等：该消息已释放过，不得再次铸造/释放。
case err != nil:
    return err
}
// release.Amount、release.AssetID、release.Recipient 可交给目标链执行。
```

`Verify` 执行相同检查但不消费消息，适合在最终化前做预校验。`Process` 才是一次性释放边界。

## 关键 API

| API | 作用 |
| --- | --- |
| `NewLightClient(trusted, finalityDepth)` | 从可信检查点初始化头链验证 |
| `LightClient.AddHeader(header, signatures)` | 验证 >2/3 共识并接入头链 |
| `LightClient.View(height)` | 原子读取规范头、head、最终化高度和冻结状态 |
| `NewVerifier(config)` | 绑定源链、目标链、策略与重放存储 |
| `Verifier.Verify(ctx, proof)` | 完整验证，不消费消息 |
| `Verifier.Process(ctx, proof)` | 完整验证 + 原子幂等消费，返回一次性释放授权 |
| `NewMemoryStore()` / `OpenFileStore(path)` | 内存/持久化重放存储 |

## 最终性与重组策略

- 检查点是可信且最终的；普通头必须逐高度连接已知父头。
- 仅严格更高的分支会成为规范链；同高度或更短分支只被保存，不影响规范状态。
- 新分支与规范链的共同祖先低于 `FinalizedHeight` 时视为最终性违规，客户端冻结，所有后续 `Verify`/`Process` 都失败，直到管理员用 `Reset` 安装新检查点。
- `Policy.AllowUnfinalized` 是显式降级选项，会牺牲重组安全，仅应在有目标链回滚/补偿机制时使用。

## 存储与集成

- `MemoryStore` 仅适合测试；生产环境应实现 `Store`，把消息键与目标链释放操作写入同一数据库事务，保证“消费 + 释放”原子性。`OpenFileStore` 是可用的单进程、fsync 持久化参考实现。
- 验证器只授权“每个已认证 lock 事件一次释放”，不能替代源链托管检查。源链必须保证 `lock` 事件发生时资产确实已锁定。
- 同一目标链存在多个桥实例时，应为每个实例配置唯一 `BridgeID`，并在源链 lock 事件中写入同一值；否则应确保各实例共享同一个持久化重放存储。
- 验证器使用静态验证者集合；验证者轮换或最终性事故恢复通过显式 `Reset` 新检查点完成，不应作为正常消息路径。

## 测试

```bash
go test ./...
go test -race ./...
```

测试覆盖：伪造金额/接收方/nonce、篡改交易与事件证明、旧证明重放、跨链与跨桥重放、合法释放、最终性不足、已最终化区块深度重组、最终化高度以上重组、共识票权不足/重复签名/伪造签名、并发重复提交、文件存储重启后重放，以及托管余额守恒（释放总额不超过锁定总额）。
