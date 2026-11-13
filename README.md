# RSA 盲签名兑换事务包

`example.org/netsec/q0061` 提供可导入的 Go 包，用于把一张授权票据安全地兑换为 RSA 盲签名。票据允许最多 `m` 次兑换，`1 <= m <= 16`；网络重试、并发重试和进程崩溃恢复都不会增加可成功解盲的不同消息数。

## 安全模型

- 签名宿主固定 RSA 公钥 `(N, e)`，私钥 `d` 不进入本包。
- 私钥操作回调只接收盲值 `b`，并且只应计算 `b^d mod N`。
- 客户端提交 128 位 `TicketID`、32 字节 `ClaimDigest`、盲值 `b` 和 `RequestID`。
- 首次请求要求 `1 < b < N` 且 `gcd(b, N) = 1`。
- 注入的 `TicketVerifier` 负责确认票据与 `TicketID`、`ClaimDigest`、截止时间及最大兑换数 `m` 的绑定关系。
- 同一 `TicketID` 下，持久记录以 `RequestID` 为键，保存盲值摘要、序号和签名结果。
- 已完成或已预留的请求按 `TicketID + RequestID + ClaimDigest + 盲值摘要` 恢复；改变盲值会被拒绝，已占用的额度不会归还。
- 超过 `m` 个不同 `RequestID` 会被拒绝；同一请求重复提交返回已持久化的同一个结果。

## 两阶段持久化

`Issuer.Redeem` 对首次请求严格执行：

1. 调用 `TicketVerifier` 验票并检查截止时间和 `m`。
2. 调用 `RedemptionStore.Reserve`，原子地分配序号并持久化盲值摘要。此步骤不保存签名。
3. 调用私钥操作回调。
4. 调用 `RedemptionStore.CommitSignature`，在独立的第二次原子提交中保存签名。

`Reserve` 与 `CommitSignature` 不会被视为同一事务。签名回调失败、返回空值或返回不满足 `s^e mod N = b` 的值时，不会提交或返回部分结果，预留记录仍然存在，可用同 `RequestID` 和同盲值重试。

`MemoryStore` 是进程内参考实现，适合测试。生产宿主应实现 `RedemptionStore`，保证每个方法在数据库中分别原子提交，并在多个签发器实例之间共享同一持久存储。

## 使用示例

```go
issuer, err := netsec.NewIssuer(netsec.IssuerConfig{
	N: publicKey.N,
	E: publicKey.E,
	Sign: func(blinded *big.Int) (*big.Int, error) {
		return new(big.Int).Exp(blinded, privateD, publicKey.N), nil
	},
	Verifier: netsec.TicketVerifierFunc(
		func(ctx context.Context, id netsec.TicketID, claim netsec.ClaimDigest, now time.Time) (netsec.TicketPolicy, error) {
			// 宿主在这里完成票据验签和绑定关系检查。
			return netsec.TicketPolicy{ExpiresAt: ticketExpiry, MaxRedemptions: ticketMax}, nil
		},
	),
	Store: durableStore,
})
```

客户端使用随机可逆因子 `r`：

```go
factor, err := netsec.NewBlindingFactor(publicKey.N)
blind, err := netsec.BlindMessage(message, factor, publicKey.N, publicKey.E)

result, err := issuer.Redeem(ctx, netsec.RedemptionRequest{
	TicketID:    ticketID,
	ClaimDigest: claimDigest,
	Blind:       blind,
	RequestID:   requestID,
})

unblinded, err := netsec.Unblind(result.Signature, factor, publicKey.N)
if !netsec.VerifyWithPublicKey(publicKey, message, unblinded) {
	// 签名无效
}
```

`BlindMessage` 计算 `b = m * r^e mod N`，`Unblind` 计算 `s * r^-1 mod N`，`VerifyWithPublicKey` 核对 RSA 签名关系。

## 测试

测试覆盖：

- 首次兑换、重复请求、同 `RequestID` 换盲值、超额兑换。
- `Reserve` 前/后和 `CommitSignature` 前/后的逐点故障恢复。
- 私钥回调失败和非法签名输出不会泄露或持久化部分结果。
- 同票据并发重试、不同请求并发抢占额度、多个签发器共享存储。
- 票据复制、跨签发器并发及请求冲突下，成功解盲的不同消息数不超过 `m`。

运行：

```sh
go test ./...
go test -race ./...
```
