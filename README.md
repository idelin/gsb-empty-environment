# Monero输入环成员与密钥映像准入

Monero 交易输入以环签名隐藏真实花费，攻击者可复用密钥映像双花，选取未解锁的输出做环成员，或构造偏移列表让环成员重复。给Go节点写供交易输入校验调用的验证包，环签名与群阶检查由调用方注入：环大小必须等于 16；key_offsets 为相对偏移，还原绝对索引后须严格递增无重复；每个环成员须是链上已存在且已解锁的输出，普通输出需 10 个区块后解锁，coinbase 输出需 60 个区块；密钥映像须通过群阶检查，在已花费集合中出现拒绝，同一交易内重复拒绝；内存池里已有相同密钥映像的交易拒绝。用重复偏移、未解锁成员、密钥映像双花、交易内重复和环大小 15 与 17 验证。

## 包说明

`netsec` 包供节点交易准入流程调用，入口为 `Validator.Validate`。
环签名验证与密钥映像群阶检查通过依赖注入，节点侧实现并传入：

- `GroupOrderChecker`：校验密钥映像是否在椭圆曲线群阶之内。
- `RingSignatureVerifier`：校验输入环签名，收到还原后的 16 个环成员。
- `ChainState`：提供链尖高度、输出查询、已花费集合与内存池查询。

### 校验规则

- 环大小必须恰好为 `RequiredRingSize`（16），15 或 17 均拒绝。
- `KeyOffsets` 为相对偏移，还原为绝对索引后必须严格递增、无重复；
  首个偏移即首个绝对索引，后续零偏移与累加溢出均拒绝。
- 每个环成员必须是链上已存在的输出，且已度过锁定期：
  普通输出 10 个区块（`NormalUnlockBlocks`），coinbase 60 个区块
  （`CoinbaseUnlockBlocks`），按 `blockHeight + 阈值 <= 当前高度` 判定。
- 密钥映像先过群阶检查，再拒绝交易内重复、已花费集合命中和内存池冲突。
- 最后调用注入的环签名验证器。

所有失败均返回包裹哨兵错误（如 `ErrRingSize`、`ErrOutputLocked`、
`ErrKeyImageSpent`）的 error，可用 `errors.Is` 匹配。

### 用法示例

```go
v, err := netsec.NewValidator(chain, groupOrderChecker, ringSigVerifier)
if err != nil {
    return err
}
if err := v.Validate(&netsec.Transaction{Inputs: inputs}); err != nil {
    switch {
    case errors.Is(err, netsec.ErrRingSize):
        // 环大小非法
    case errors.Is(err, netsec.ErrKeyImageSpent):
        // 双花
    default:
        // 其余准入失败
    }
}
```

### 测试

`validate_test.go` 覆盖重复偏移、普通与 coinbase 未解锁成员、
密钥映像双花、交易内重复、内存池冲突、群阶失败、环签名失败，
以及环大小 15 与 17 等场景，并包含解锁边界与全量通过用例。
