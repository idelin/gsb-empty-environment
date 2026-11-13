# 双人 HSM 根密钥导入仪式

该包实现一个内存态导入仪式状态机，防止同一自然人用多个 credentialID
凑票，也防止批准或密钥份额跨仪式重放。

## 安全约束

- 管理员条目必须由 HR 根 Ed25519 密钥签名。一个 credentialID 一旦绑定
  `PersonID` 就不可改绑，同一公钥也不能登记给另一个自然人。
- 仪式绑定随机 `ceremonyID`、槽位、算法、用途、密钥材料 SHA-256 承诺、
  一次性包装公钥摘要、开始/截止时刻和策略代次。
- 批准签名覆盖完整仪式绑定、credentialID、注册表中的不可变 `PersonID`
  以及 `decision=approve`。旧仪式的批准无法在新仪式通过验证。
- 票数按 `PersonID` 去重；同一人的多个凭证合计最多一票、一份份额。
- 策略阈值至少为 2，并可要求 `SecurityOfficer` 与 `KeyCustodian` 同时
  参与。`RoleAdministrator` 不能成为导入批准角色。
- 份额在参与者侧使用 HSM 的一次性 X25519 导入公钥加密，AAD 绑定完整
  仪式和 `PersonID`，并用参与者 Ed25519 credential 签名。宿主只保存和
  转发密文封装，不接收或保存明文份额。
- 所有已计票参与者都必须提交一份份额。提交给 HSM 的份额按 `PersonID`
  的字节序升序排列，由 HSM 解密后逐字节异或。
- `Complete` 通过状态转移保证每个仪式最多调用一次 `CombineAndImport`。
  并发调用者等待同一个结果，不会重复导入。
- 只有 HSM 返回的 keyHandle、属性摘要、实际密钥材料承诺和 HSM 签名收据
  全部与仪式绑定一致时才进入 `completed`。
- 截止时间、批准撤销、策略代次变化或导入失败都会终止仪式并清除封装的
  份额。丢失 `CombineAndImport` 响应时，使用 `ceremonyID` 调用
  `ReadReceipt`；回读仍会重新检查截止时间、撤销和策略代次。

## 使用方式

```go
policy := netsec.DefaultPolicy(1) // 2 人，且 SO/KC 各至少一人
registry, err := netsec.NewRegistry(hrRootPublicKey, policy)
// registry.Register(signedCredential) ...

host, err := netsec.NewHost(registry, hsm, hsmReceiptPublicKey, nil)
ceremony, err := host.BeginCeremony(ctx, netsec.CeremonySpec{
    Slot:                  "slot-7",
    Algorithm:             "AES-256-GCM",
    Purpose:               "root-key",
    KeyMaterialCommitment: sha256.Sum256(finalKeyMaterial[:]),
    Validity:              10 * time.Minute,
})

binding := ceremony.Binding()
approval, err := netsec.SignApproval(
    binding, credentialID, personID, adminPrivateKey,
)
err = ceremony.Approve(ctx, approval)

share, err := netsec.GenerateShare()
sealed, err := netsec.SealShare(
    binding,
    ceremony.WrappingPublicKey(),
    credentialID,
    personID,
    adminPrivateKey,
    share,
)
err = ceremony.SubmitShare(ctx, *sealed)

result, err := ceremony.Complete(ctx)
// 若回调响应丢失：
result, err = ceremony.ReadReceipt(ctx)
```

`SealShare` 应由参与者在自己的信任域调用。宿主只需要接收返回的
`SealedShare`，不应接收 32 字节明文份额。

## HSM 契约

`HSM` 接口包含三个方法：

- `CreateImportKey`：为单个 `ceremonyID` 生成一次性 X25519 导入密钥，
  返回公钥并只把私钥留在 HSM 内。
- `CombineAndImport`：按给定顺序打开 `WrappedShare`，验证 AAD 和
  `personID`，异或得到密钥材料，验证 SHA-256 承诺，导入后返回
  keyHandle、`AttributesDigest(binding)` 和签名收据。
- `ReadReceipt`：按 `ceremonyID` 幂等返回首次导入的收据。丢失响应时，
  不得再次执行导入。

收据签名使用 `ReceiptSigningPayload`。属性摘要应使用
`AttributesDigest`，它覆盖完整绑定，包括槽位、算法、用途、材料承诺、
包装公钥摘要和策略代次。

## 测试

`go test -race ./...` 使用真实 Ed25519、X25519 和 AES-GCM，以及只在内部
持有导入私钥的 HSM 替身，覆盖：

- 同人别名凭证重复投票；
- 旧批准重放到新仪式；
- 份额跨仪式移植；
- 必需角色缺失；
- 未批准者提交的额外份额不能虚增份额阈值；
- 24 个并发 `Complete` 只触发一次 HSM 导入；
- HSM 已提交但响应丢失时按 `ceremonyID` 回读；
- 回读不能绕过撤销、策略变化或超时；
- HSM 导入期间发生策略变化不会在回调返回后误标完成；
- 收据槽位不匹配时不会完成；
- 阈值小于 2 的策略被拒绝；
- HR 根签名篡改和 credentialID 改绑。
