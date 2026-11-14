# 同名证书主体映射到了错误租户

网关按证书CN把请求映射到租户，攻击者申请到同名CN的另一张合法证书，访问该租户资源。请实现供Go网关调用的可导入证书主体映射组件。映射优先使用唯一标识与SAN，CN只在唯一注册且签发者受信时使用；同主体多证书按指纹登记，未登记即拒绝。签发者、策略OID与撤销状态一并核对，映射变更按代次。用同名CN、伪造签发者、未登记证书、合法唯一映射和轮换验证身份不歧义。

## 实现

`example.org/netsec/q0365` 是一个无外部依赖的 Go 包。它不按主体名称直接查表，而是把“已登记证书”作为授权边界：

1. **指纹登记**：每个叶子证书的 DER SHA-256 指纹必须显式登记到租户。未登记证书在任何身份匹配之前即被拒绝。
2. **身份优先级**：先使用唯一主体属性（`serialNumber`、`x500UniqueIdentifier`、`organizationIdentifier`、`UID`，以及 `Options.UniqueIdentifierOIDs` 配置的 OID），再使用 SAN（DNS、URI、email、IP），最后才回退 CN。只要存在 SAN 扩展，就不会因 SAN 不可识别而回退 CN。
3. **CN 唯一性**：CN 仅在该名称在整个映射代次中只登记给一个租户时可用；同名 CN 分属不同租户时返回 `ErrCNNotUnique`，多证书同属一个租户时仍可回退。
4. **签发者校验**：使用 `x509.Verify` 把叶子证书验证到显式信任根。仅复制受信 CA 的 Issuer 名称无法通过签名和路径校验。
5. **策略与撤销**：可要求证书携带指定策略 OID、限制允许的策略集合；撤销检查覆盖已验证路径中的所有非根证书。未配置撤销检查器时失败关闭。
6. **代次发布**：`Register`、`RegisterAll`、`Rotate`、`Replace` 原子发布不可变快照。一次解析只使用一个代次，返回的 `Mapping.Generation` 可用于审计。轮换后旧证书立即从新代次移除，已取得旧快照的读取者仍看到一致的旧视图。

## 使用

```go
roots := x509.NewCertPool()
roots.AddCert(trustedCA)

revocations := netsec.NewRevocationSet()
mapper, err := netsec.NewRegistry(netsec.Options{
	Roots:              roots,
	Revocation:         revocations,
	RequiredPolicyOIDs: []asn1.ObjectIdentifier{{1, 3, 6, 1, 4, 1, 55555, 1}},
})
if err != nil {
	return err
}

// 每个合法证书都按指纹登记；同一租户可登记多张并存的证书。
if _, err := mapper.Register(netsec.Registration{
	Tenant:      "tenant-a",
	Certificate: leaf,
}); err != nil {
	return err
}

// 网关从 TLS 连接取得叶子证书和中间证书后调用。
mapping, err := mapper.Resolve(ctx, leaf, intermediates...)
if err != nil {
	return err
}
tenant := mapping.Tenant
generation := mapping.Generation

// 轮换在一次原子代次变更中替换该租户的全部证书。
_, err = mapper.Rotate("tenant-a", nextLeaf)
```

`Registration.PolicyOIDs` 可为单张证书追加必须满足的策略。`Snapshot`、`Add`、`RegisterAll`、`Replace`、`RemoveTenant` 和 `RemoveCertificate` 提供相应的运维能力。

## 验证

```sh
go test ./...
go test -race ./...
```

测试使用真实生成的证书链覆盖：同名 CN 的不同租户隔离、SAN/唯一标识优先、CN 唯一回退、伪造同名签发者、未登记证书、合法唯一映射、策略 OID、撤销状态，以及多证书原子轮换和代次快照一致性。
