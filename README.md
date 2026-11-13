# DSA 证书路径参数继承

本模块提供一个可导入的 Go 证书路径验签库，处理 DSA 公钥参数（p、q、g）
在证书链中的继承与一致性。

核心规则：

- 子证书省略 DSA 参数时，从签发证书继承当前有效的参数。
- 子证书携带 DSA 参数时，必须与签发证书的参数完全相同。
- 链中途换用另一组参数会失败，返回 `ErrDSAParameterMismatch`。
- 子证书签名算法 `AlgorithmIdentifier` 中的参数同样只能省略或与签发者一致。
- 根证书是信任锚，必须携带参数；根证书自身签名不作为路径链接校验。

`crypto/x509` 可以解析 DSA 证书但不能验签，也不能解析省略
`SubjectPublicKeyInfo` 参数的 DSA 证书。本库自行解析所需 ASN.1 字段并调用
`crypto/dsa` 验签，因此同时支持这两种省略参数的位置。

## API

```go
import netsec "example.org/netsec/q0293"

// 证书可按叶到根或根到叶顺序提供。
err := netsec.VerifyChain(certs)
err = netsec.VerifyChain(leaf, intermediate, root)

// 需要支持省略 SubjectPublicKeyInfo 参数的 DSA 证书时使用 DER 入口。
err = netsec.VerifyDERChain([][]byte{leafDER, intermediateDER, rootDER})

// 或直接校验 PEM 证书串。
err = netsec.VerifyPEMChain(pemData)
```

`VerifyChain`、`VerifyDSAChain` 既接受 `[]*x509.Certificate`，也接受逐个
证书参数。`Chain`/`NewChain` 提供带 `Verify()` 方法的包装，`VerifyPath`、
`VerifyCertificatePath`、`VerifyCertificateChain` 和 `ValidateChain`
是切片入口的兼容别名。

## 范围

该库执行密码学路径校验（逐张证书验签及 DSA 参数继承/一致性），不执行
RFC 5280 的完整策略检查（例如有效期、名称约束、用途和 CA 位）。调用方可
在验签前自行执行这些业务策略检查。

## 测试

```sh
go test ./...
```

测试覆盖省略参数继承、显式相同参数、签名参数被替换、子证书公钥参数被替换、
三层链、PEM 输入、根证书缺参数以及 RSA 链回归场景。
