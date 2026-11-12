# StoreKit JWS证书链与OID

## 用法

校验逻辑位于可导入子包 `appstorejws`，调用方需内置 Apple 根证书的 DER
字节、本应用 `bundleId` 以及服务端当前环境：

```go
import "example.org/netsec/q0362/appstorejws"

verifier, err := appstorejws.New(appstorejws.Config{
    RootDER:     appleRootDER, // 随服务内置的 Apple Root CA DER
    BundleID:    "com.example.app",
    Environment: appstorejws.EnvironmentProduction, // 或 EnvironmentSandbox
})
if err != nil {
    return err
}

tx, err := verifier.Verify(signedTransactionInfo)
switch {
case errors.Is(err, appstorejws.ErrRevoked):
    // tx 仍会返回，按已撤销交易处理（如退款/封禁）
case err != nil:
    // 拒绝该交易，可用 errors.Is 区分具体失败原因
default:
    // tx 为校验通过的交易信息，tx.Raw 保留完整载荷
}
```

校验顺序为：紧凑 JWS 结构、`alg=ES256`、`x5c` 恰为三张 DER 证书、
根证书与内置 DER 逐字节相同、叶/中间证书标记 OID、链签名（叶由中间签、
中间由根签、根自签）、按 `signedDate` 检查三张证书有效期、ES256 签名验签、
`bundleId`/`environment` 声明匹配、`revocationDate` 撤销判定。

测试用内存生成的 P-256 证书链覆盖：攻击者自签链、共享根但伪造中间签名、
叶/中间缺 OID、`NotBefore`/`NotAfter` 边界、沙箱与生产环境交叉校验、
`revocationDate` 撤销、载荷与签名篡改、畸形 JWS 等场景，运行 `go test ./...`。

服务端验证 App Store 的 signedTransactionInfo，攻击者自建证书链签出一份看似合法的交易。为Go服务实现可导入的 JWS 校验包：头里 alg 只许 ES256，x5c 恰为三张证书（叶、中间、根），根须与调用方内置的 Apple 根证书 DER 完全相同；证书链按载荷 signedDate 时刻验证有效期；叶证书须带 OID 1.2.840.113635.100.6.11.1，中间证书须带 1.2.840.113635.100.6.2.1；载荷里 bundleId 须等于本应用，environment 与服务端当前环境一致，存在 revocationDate 则判为已撤销。用自签链、缺 OID、有效期边界与沙箱交易验证。
