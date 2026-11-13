# 注册OID约束

本模块提供一个可导入的 Go 库，用于在证书路径校验中处理 RFC 5280
`registeredID`（`GeneralName` 标签 8）名称约束。Go 标准库
`crypto/x509` 不会暴露 `registeredID` SAN 或 `registeredID` 名称约束，
因此本模块直接解析原始 `subjectAltName`（2.5.29.17）和
`nameConstraints`（2.5.29.30）扩展。

匹配规则：

- `registeredID` 只和 OID 完全相同的注册标识匹配。
- 严格模式（`PolicyStrict`）下只有同型 `registeredID` 能匹配该约束；
  DNS、邮箱和 URI 都不能顶替。
- 兼容模式（`PolicyLegacy`，默认）下，一个完整值为点分十进制 OID 的 DNS
  SAN 可以满足同一 OID 的 `registeredID` 约束。例如 DNS SAN
  `1.2.3.4` 满足 permitted `registeredID` `1.2.3.4`。邮箱和 URI 不参与
  这条兼容路径。

主要 API：

```go
nc, err := netsec.ParseNameConstraints(caCert)
names, err := netsec.ParseSubjectAltNames(leafCert)
err = netsec.CheckNameConstraints(chain, netsec.PolicyLegacy) // leaf 在前
err = netsec.VerifyChain(chain, netsec.Options{Policy: netsec.PolicyLegacy})
err = netsec.VerifyRegisteredID(leafCert, oid, netsec.PolicyLegacy)
```

`MarshalNameConstraints`、`NameConstraintsExtension` 和
`SubjectAltNameExtension` 可用于构造带 `registeredID` 约束的测试证书。

命令行示例：

```sh
go run ./cmd/q0288 -root root.pem -intermediate int.pem -leaf leaf.pem -oid 1.2.3.4
```
