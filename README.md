# 多账号密钥别名冲突

相同密钥别名在不同账号指向不同材料，构建服务不得把别名当全局身份。给Go密钥宿主实现引用解析包，解析绑定账号、区域、别名版本和最终公钥指纹，别名链可多跳但不得跨未授权账号；同一事务固定所有解析结果。用环、同名异钥及解析途中重绑定验证生成的签名请求对象。

## 设计

- 别名身份是完全限定的 `AliasRef{Account, Region, Name}`；同名别名在不同账号或区域是不同引用，可指向不同密钥。
- `Resolver.Begin` 开启事务：事务持有不可变 `StoreSnapshot` 与逐引用结果缓存，事务内所有 `Resolve` 看到相同的绑定版本、跳转链和最终指纹；并发重绑定不影响已开启的事务，新事务才能看到新版本。
- 解析逐跳执行：每一跳都校验调用方 `CallerIdentity` 的授权账号集合，跨未授权账号返回 `ErrAccountNotAuthorized`；环返回 `ErrCycle`（含闭环路径），链长超过上限返回 `ErrMaxHops`。
- `Resolution` 记录入口别名、入口版本、逐跳版本链、最终账号/区域/密钥 ID 与公钥 SHA-256 指纹。
- `BuildSignRequest` 基于固定的解析结果生成 `SignRequest`，对象绑定最终公钥指纹而非裸别名；`Canonical`/`Digest` 提供确定性编码，`Verify` 检测指纹调换、版本篡改和环。

## 典型用法

```go
store := netsec.NewMemoryStore()
store.PutKey(netsec.PublicKey{Account: "acct-a", ID: "key-a", DER: derBytes})
store.Rebind(
    netsec.AliasRef{Account: "acct-a", Region: "cn-north-1", Name: "signing-key"},
    netsec.Target{Kind: netsec.TargetKey, Key: "key-a"},
)

caller := netsec.NewCallerIdentity("builder-1", []netsec.AccountID{"acct-a"})
txn, _ := netsec.NewResolver(store).Begin(ctx, caller)
res, err := txn.Resolve(ctx, netsec.AliasRef{Account: "acct-a", Region: "cn-north-1", Name: "signing-key"})

req, err := netsec.BuildSignRequest(caller, res, netsec.WithPayload(artifact))
digest, err := req.Digest() // 交给密钥宿主签名的内容摘要
```

## 测试

- 多跳链、同名异钥（跨账号/跨区域）、跨区域与授权跨账号跳转。
- 环与自环检测、跳数上限、未授权跨账号拒绝、别名/密钥缺失、非法引用。
- 解析途中重绑定：通过 hook 在链遍历过程中重绑定，验证事务仍固定旧版本与旧指纹，新事务才观察到重绑定。
- 签名请求规范化编码往返、指纹调换/DER 篡改/版本篡改/环检测、并发解析（`-race`）。

运行：`go test -race ./...`
