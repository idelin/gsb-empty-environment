# CCA领域令牌与平台令牌绑定

Arm CCA 的 realm 令牌须与平台令牌属于同一设备，否则攻击者可以把另一台
机器上合法的平台令牌拼接到当前 realm 令牌。本包在**两个 COSE_Sign1 签名
均已验签通过之后**执行绑定检查：

- realm 令牌的 challenge（EAT nonce，claim key 10）等于调用方 nonce；
- platform 令牌的 challenge 等于 realm 公钥按
  `cca-realm-public-key-hash-algo-id` 计算的哈希；
- realm 测量哈希算法与公钥哈希算法均在允许集内（默认 SHA-256/384/512，
  拒绝 SHA-1/MD5）；
- `cca-realm-initial-measurement` 与每个
  `cca-realm-extensible-measurements` 的长度等于哈希算法的摘要长度。

签名验证不在此包内完成，调用方必须先验证两个 COSE_Sign1 的签名。

## 用法

```go
import netsec "example.org/netsec/q0095"

// realmToken 和 platformToken 是已验签的原始 COSE_Sign1 字节。
if err := netsec.VerifyBinding(realmToken, platformToken, callerNonce); err != nil {
    return err
}
```

也支持直接传入已解码的 claim map、`Claims` 值、带 claim 字段的结构体，或
包含 `44234`（platform）与 `44241`（realm）的 CCA collection。参数顺序可
交换；第四个及后续参数可以传 `Policy`、`Option`、`[]string` 或
`[]HashAlgorithm` 来收敛允许算法集合。

错误可用 `errors.Is` 判断，例如 `ErrChallengeMismatch`、
`ErrAlgorithmNotAllowed`、`ErrMeasurementLength`、`ErrMissingClaim`。

`binding` 子包提供同一组类型和函数，便于按包名导入。

## 测试

```sh
go test ./...
```

测试覆盖原始 COSE_Sign1、CCA collection、拼接不同设备的平台令牌、调用方
nonce 不符、初始/扩展测量长度错误、算法不在允许集以及 base64 claim 输入。
