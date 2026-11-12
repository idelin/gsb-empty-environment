# Hypercore 签名树头回滚与等同证据

Hypercore 式追加日志由写者对树头签名，攻击者可重放较早的已签名树头让读者看到被截断的历史，或让同一长度对应两个不同树根。给Go读者库写供复制流程调用的树头校验包，签名验证由调用方注入，哈希按 RFC 9162 的叶子 0x00、内部节点 0x01 规则：读者记录已验证的（fork，长度，树根）；新树头同一 fork 下长度小于已知时拒绝；长度相同但树根不同视为写者等同伪造，冻结该日志并保存两份已签名树头作为证据；fork 号增大须有签名覆盖该 fork，且重新从零验证；区块只在其包含证明能还原到已签名树根时才存入。用回滚树头、同长度异根、fork 切换和错误包含证明验证。

## API

```go
reader := netsec.NewReader(func(head netsec.TreeHead, sig []byte) error {
    // 调用方实现的签名验证。必须认证 head 的 Fork、Length、Root 全部字段。
    return verifySignature(head.SigningBytes(), sig)
})

// 复制流程收到签名树头后先校验，再决定是否采用。
err := reader.Accept(signedHead)

// 只为能还原到当前已签名树根的区块调用此方法。
err = reader.AddBlock(index, block, inclusionProof)
```

`Accept` 的状态规则：

- 首个签名有效的树头建立 `(fork, length, root)` 状态。
- 同一 fork 下长度变小：`ErrRollback`，状态不变。
- 同一 fork、同一长度、树根不同：`ErrEquivocation`。读者冻结，`Evidence()`
  保存两份完整的已签名树头，后续树头和区块更新均返回 `ErrFrozen`。
- fork 号变大：必须先通过签名验证。读者丢弃旧 fork 的区块和长度状态，从新
  fork 的树头重新开始验证。改过 `Fork` 字段但没有重新签名的旧树头会得到
  `ErrInvalidSignature`。
- fork 号变小：`ErrForkRollback`。
- 相同树头可重复提交，操作幂等。

`AddBlock` 仅在 `index < Length` 且包含证明按 RFC 9162 规则折叠到当前已签名
`Root` 时保存区块，否则返回 `ErrInclusionProof` 或 `ErrBlockRange`，不会写入
任何数据。已保存的不同区块内容不会被覆盖。

## RFC 9162 哈希与证明

- 叶子：`SHA-256(0x00 || block)`，见 `LeafHash`。
- 内部节点：`SHA-256(0x01 || left || right)`，见 `NodeHash`。
- 空树：`SHA-256("")`，见 `EmptyRoot`。
- `MerkleRoot` / `RootFromHashes` 按最大小于 `n` 的二次幂切分。
- `ProveInclusion` 生成从叶层向上的兄弟子树根；`VerifyInclusion` 校验新区块。

`SignatureVerifier` 接收完整 `TreeHead`。为便于使用，`TreeHead.SigningBytes`
提供带域分隔的 fork/length/root 规范编码；调用方也可使用自己的签名编码，但
必须保证 fork 号被签名覆盖。

## 测试

```sh
go test ./...
```

测试覆盖回滚树头、同长度异根与证据保留、fork 切换/旧 fork 重放/状态清零、
错误包含证明不落盘，以及 1 到 40 个叶子的所有包含证明形状。
