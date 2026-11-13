# MSO摘要与值分离

移动安全对象里的摘要对上了，但出示的值是另一段字节。每个出示值的摘要都必须等于MSO中对应项；只验MSO签名而不验值则不够。值与摘要一致且MSO签名正确时接受。可导入的Go库供钱包调用。保持MSO，替换一个出示值。

## 用法

```go
import netsec "example.org/netsec/q0186"

elements, err := netsec.VerifyDeviceResponse(deviceResponseCBOR, &netsec.Options{
    Roots: issuerRoots, // 或 IssuerKey: pinnedKey
})
```

`VerifyDeviceResponse` 只有在两个条件同时满足时才接受文档：

- `issuerAuth` 的 COSE_Sign1 签名对 MSO 验签通过（密钥来自钉住的
  `IssuerKey`，或经 `Roots` 验证的 x5chain 证书链）；
- 每个出示的 `IssuerSignedItem` 的摘要等于 MSO `valueDigests` 中
  对应 `(namespace, digestID)` 的条目。

攻击者保持 MSO 不变、替换某个出示值时，摘要不再匹配，验证以
`ErrDigestMismatch` 失败；若同时篡改 MSO 中的摘要，则签名验签以
`ErrMSOSignature` 失败。错误可用 `errors.Is` 区分：
`ErrMSOSignature`、`ErrDigestMismatch`、`ErrUnknownDigestID`、
`ErrNoIssuerKey`、`ErrExpired`。
