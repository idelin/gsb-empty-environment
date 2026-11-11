# 代理证书链逐跳归属

企业代理串联多层TLS终止点，最终服务需要知道哪一跳真正验证了客户端证书。写供Go接入宿主调用的证明链包，各跳签名绑定前一证明摘要、已验证主体、目标服务、连接挑战和有效期；中间跳只能追加自己的验证事实，不能提升上游保证等级。使用标准库真实签名，拒绝断链和跨连接拼接。用两条合法链交叉拼段验证身份、受众及挑战均不可混用。

## 包结构（`package netsec`）

- `proofchain.go`：`Assurance` 保证等级、`Fact`/`Attestation` 模型、确定性 TLV
  规范编码、`GenesisDigest`、`Signer`（ECDSA P-256 / Ed25519 / RSA-PSS，均为
  标准库真实签名）。
- `chain.go`：不可变 `Chain`，`NewChain` / `Append` 只能在尾部追加，新跳签名
  绑定前一跳 `AttestationDigest`；`Encode` / `ParseChain` 使用 JSON 传输。
- `keyring.go`：宿主侧 `KeyRing`，按 `KeyID` 绑定跳 ID、算法与公钥，并完成
  签名验证。
- `verify.go`：`Verify` 逐跳执行全部绑定与时效检查，返回首个真正验证客户端
  的跳（`FirstVerifier`）、客户端身份与全链有效保证等级。

## 签名绑定内容

每条证明对确定性 TLV 字节串签名，覆盖：前一证明摘要（根跳为绑定
`audience|challenge` 的创世摘要）、已验证主体、目标服务（受众）、连接挑战、
`NotBefore/NotAfter` 有效期、本跳保证等级与 `UpstreamMin`。

## 关键安全规则

- 中间跳只能追加：`Chain` 不暴露插入/改写接口，追加时校验索引、前一跳摘要、
  受众与挑战一致，并即时验签。
- 不能提升上游等级：校验器要求每跳 `UpstreamMin` 严格等于此前各跳最小值；
  全链 `EffectiveAssurance` 取所有跳的最小值。
- 拒绝断链：`PrevDigest` 不匹配即 `ErrBrokenChain`。
- 拒绝跨连接拼接：挑战和受众被签入每个载荷，重签也无法改写；被验证主体
  必须是本链中已出现的跳，跨链同 ID 拼段触发 `ErrIdentityMismatch`。
- 签名密钥与跳身份绑定：钥环中 `KeyID` 归属的 `HopID` 与证明不一致时拒绝。

## 测试

- `splicing_test.go`：构造两条独立的合法三跳链（不同客户端、受众、挑战与
  跳密钥），交叉拼段后分别验证断链、挑战、受众、身份均不可混用。
- `security_test.go` / `api_test.go`：篡改签名、未知/错配密钥、有效期、
  保证等级提升、创世绑定、不可变性、编解码往返与各类错误路径。

运行：

```sh
go test -race -cover ./...
```
