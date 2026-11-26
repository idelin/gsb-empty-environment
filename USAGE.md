# Threshold DKG and Accountable FROST Signing

根包 `threshold` 是可导入的门限 DKG/FROST 状态机。

## 组件

- `Group`：抽象素数阶群接口；`P256Group()` 提供真实 P-256 实现。
- `Participant`：两轮 Feldman DKG 参与方。
- `Coordinator`：校验第一轮 PoP、验真投诉、维护永久 `Blacklist` 并冻结 `DKGPublicKey`。
- `NonceVault` / `NonceBundle`：预生成 `(D_i,E_i)`，并保证对应私钥 nonce 只能原子消费一次。
- `Signer` / `SigningSession`：FROST 绑定因子、两轮签名、逐份验签、聚合和精准追责。

## DKG

1. 所有参与方使用相同的 `DKGConfig`，其中包含 `Context`、`(t,n)`、有序且非零的 `Identity.Index`。
2. 每个 `Participant.Round1()` 产出长度为 `t` 的 Feldman 承诺和绑定 `(context, participantID, C_0)` 的 Schnorr PoP。
3. `Coordinator.HandleRound1` 严格验证承诺长度、点编码和 PoP；无效发送者进入黑名单。
4. `Participant.CreateShares()` 为每个接收方生成秘密份额。宿主必须通过认证加密的点对点通道发送 `Round2Share`。
5. 接收方调用 `ReceiveShare`。坏份额会返回非空 `*Complaint` 和 `ErrInvalidShare`；任何第三方可用 `Complaint.Verify(group)` 独立验真。
6. `Coordinator.SubmitComplaint` 核对规范承诺并验真后，将作恶节点加入并发安全的永久黑名单。
7. 合格人数至少为 `t` 时，`Coordinator.Freeze` 输出联合公钥和各节点公钥份额；参与方使用相同合格集合调用 `Finish` 得到 `KeyPackage`。

## FROST Signing

1. `Signer.Nonces.Generate()` 预生成一次性 `(D_i,E_i)`。
2. 聚合器 `AddCommitment` 收集签名集合的全部承诺，`FinalizeCommitments` 冻结规范列表 `B`。
3. `rho_i = H_1(i, msg, B)` 绑定消息、签名者序号以及全部 `(D_j,E_j)`，防止在承诺集合上进行 Wagner 并发生日操作。
4. `Signer.SignWithBundle` 在互斥临界区内将 nonce 标记为已消费、从保险库删除并清零存储，然后才计算部分签名。同一 bundle 的并发调用恰好一次成功，其余得到 `ErrNonceReuse`。
5. `SigningSession.Aggregate` 逐份执行
   `z_i G = D_i + rho_i E_i + lambda_i c Y_i`。
   失败时返回 `*AccountabilityError`，其 `Report.Culprits` 是精确作恶 ID，而不只是一个验签失败布尔值。
6. 成功聚合后可用 `VerifySignature` 验证 `zG = R + cY`。

## 测试

```bash
go test -v ./...
go test -race ./...
```

测试使用真实 P-256 群覆盖：

- `(1,1)`、`(2,3)`、`(3,5)` 诚实门限签发；
- Rogue-Key 公钥抵消因缺少有效 PoP 被拦截；
- 坏 Feldman 份额生成可独立验真投诉，作恶者被永久拉黑和剔除，剩余门限仍可签发；
- 32 个 goroutine 并发重放同一 nonce bundle 时恰好一次成功；
- 被篡改的部分签名只指定一个作恶者 ID；
- 修改其他签名者的 `(D,E)` 或消息会改变 `rho_i`。

生产宿主还应持久化 `Blacklist.Records()` 和已消费 nonce ID；`Round2Share` 必须运行在带发送方认证的加密通道之上。
