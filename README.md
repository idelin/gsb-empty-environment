# 工作负载身份验证组件

`example.org/netsec/q0238` 是供 Go 服务网格控制面导入的工作负载身份验证与连接授权库。它修复“把证书 DNS SAN 中的 `*.prod` 当成生产主体，导致 `evil.prod` 测试证书获得生产 mTLS 身份”的身份放大问题。

## 安全模型

工作负载主体从来不是 DNS SAN，也不是通配符匹配结果。一个可授权的 `WorkloadIdentity` 必须同时满足：

- URI SAN 是唯一的严格 SPIFFE ID：`spiffe://<trust-domain>/<path>`；
- trust domain、SPIFFE path、完整 SAN 集合都符合当前身份策略；
- 叶证书签名链验证到该 trust domain 当前代次的 trust bundle；
- 节点证明在同一 trust domain、同一 workload ID、同一建连 nonce 上完成绑定，并通过节点选择器检查；
- 调用方策略、目标策略和动作在本次建连时重新评估。

任何一项失败都会拒绝，且错误可通过 `errors.Is` 判断。

### SAN 类型隔离

- DNS SAN 只用于主机名约束，永远不会生成 SPIFFE 主体。
- URI SAN 必须是 `spiffe` scheme，禁止 `https`、userinfo、端口、query、fragment、`%` 转义和路径穿越。
- IP SAN 只在 `IPPrefixes` 命名空间中匹配；编码成 DNS 名称的 IP 字面量会被拒绝。
- 证书中出现 email SAN、未知 GeneralName、多个不同 SPIFFE URI、通配符 DNS SAN 时直接失败。
- 证书中的所有 DNS/IP SAN 都必须被策略显式允许；额外 SAN 不会扩大身份。

### 通配规则

通配符只允许出现在策略声明中：

- 必须是完整的最左标签：`*.prod`、`*.prod.example.org`；
- 禁止 `*.*.prod`、`foo.*.prod`、`*.prod.*`、`*foo.prod`、`foo*.prod`；
- 深度固定为 1 个标签，`*.prod` 不匹配 `a.b.prod`；
- 只能用于 `SANUseHostname`，不能作为 identity 或 address 使用。

因此 `*.prod` 可以匹配 `evil.prod` 这个主机名，但不会把 `evil.prod` 提升为生产 SPIFFE 主体。生产路径、trust domain、签名链和节点证明仍必须全部匹配。

### 代次与轮换

`Generation` 是单调版本号：

- `UpdateBundle`：每个 trust domain 独立代次，旧代次更新被拒绝；新 bundle 立即用于后续连接。
- `UpdateRevocations`：完整吊销列表按代次替换；旧代次不能回滚吊销状态；吊销在下次建连立即生效。
- `UpdateRotations`：short-lived 证书按 workload 记录当前 active serial；进入新代次后旧 serial 即使未过期也立即失效。
- `UpdatePolicies`：完整策略快照按代次替换，调用方和目标策略每次建连都重新评估。

每次 `AuthorizeConnection` 只读取一个不可变快照，因此一次建连中的 bundle、吊销、轮换和策略代次保持一致。多个更新通过互斥锁串行提交，读路径使用原子快照。

## 使用示例

```go
authority, err := netsec.NewAuthority(netsec.Config{
    TrustDomain: netsec.TrustDomain("prod.example.org"),
    AttestationKeys: map[string]ed25519.PublicKey{
        "node-1": nodePublicKey,
    },
    MaxCertificateLifetime: 30 * time.Minute,
    MaxAttestationAge:      5 * time.Minute,
})
if err != nil {
    return err
}

// Trust anchors, revocations, rotations, and policies are versioned.
if err := authority.UpdateBundle(
    netsec.TrustDomain("prod.example.org"),
    1,
    []*x509.Certificate{prodRoot},
); err != nil {
    return err
}

if err := authority.UpdateRevocations(netsec.TrustDomain("prod.example.org"), 1, nil); err != nil {
    return err
}

if err := authority.UpdateRotations(1, []netsec.RotationRecord{
    {
        Workload:   backendSPIFFEID,
        Generation: 1,
        Active:     []netsec.CertificateID{backendCertificateID},
        ValidUntil: now.Add(time.Hour),
    },
}); err != nil {
    return err
}

if err := authority.UpdatePolicies(1, map[string]netsec.ConnectionPolicy{
    "backend-to-db": {
        Caller: netsec.IdentityPolicy{
            TrustDomain:   "prod.example.org",
            Paths:         []string{"/ns/prod/sa/backend"},
            DNSNames:      []netsec.DNSNameRule{netsec.ExactDNSName("backend.prod.example.org")},
            NodeSelectors: map[string]string{"env": "prod"},
            Uses:          []netsec.PrincipalUse{netsec.UseClient},
        },
        Target: netsec.IdentityPolicy{
            TrustDomain:   "prod.example.org",
            Paths:         []string{"/ns/prod/sa/database"},
            DNSNames:      []netsec.DNSNameRule{netsec.ExactDNSName("db.prod.example.org")},
            NodeSelectors: map[string]string{"env": "prod"},
            Uses:          []netsec.PrincipalUse{netsec.UseServer},
        },
        Actions: []string{"sql"},
    },
}); err != nil {
    return err
}

decision, err := authority.AuthorizeConnection(ctx, netsec.ConnectionRequest{
    Policy: "backend-to-db",
    Caller: netsec.PeerCredential{
        Leaf:          callerLeaf,
        Intermediates: callerIntermediates,
        Attestation:   callerAttestation,
    },
    Target: netsec.PeerCredential{
        Leaf:          targetLeaf,
        Intermediates: targetIntermediates,
        Attestation:   targetAttestation,
    },
    Action: "sql",
    Nonce:  connectionChallenge,
})
```

如果控制面已有解码后的 PEM/DER 链，可用 `ParseCredential`；已有 `[]*x509.Certificate` 时可用 `CredentialFromChain`。需要只做身份提取而不做连接策略时可用 `VerifyPeer`，但生产建连应使用带 nonce 和双端策略的 `AuthorizeConnection`。

## 验证

```sh
go test ./...
go test -race ./...
go vet ./...
```

测试覆盖以下攻击面：

| 场景 | 测试 |
| --- | --- |
| 合法同域服务 | `TestLegitimateSameDomainWorkloadIsAccepted` |
| `evil.prod` 不获得生产主体 | `TestEvilProdWildcardDoesNotWidenPrincipal` |
| 多级通配与证书通配 SAN | `TestMultiLevelWildcardIsRejectedInPolicy`、`TestWildcardCertificateSANIsRejected` |
| URI 冒充、userinfo、端口、多 URI | `TestURIImpersonationIsRejected` |
| IP/DNS/URI 类型替换 | `TestSANTypesDoNotSubstitute` |
| 额外 SAN 放大 | `TestExtraSANsCannotAmplifyIdentity` |
| 跨 trust domain | `TestCrossTrustDomainIsNotAccepted` |
| 吊销后连接与旧代次回滚 | `TestRevocationTakesEffectOnNextConnection` |
| short-lived 轮换代次 | `TestShortLivedRotationTakesEffectByGeneration` |
| bundle 更新代次 | `TestBundleUpdateTakesEffectByGeneration` |
| 中间 CA 链与链上吊销 | `TestIntermediateChainAndChainRevocation` |
| 调用方/目标策略逐连接评估 | `TestPoliciesAreEvaluatedOnEveryConnection`、`TestTargetPolicyIsEvaluatedOnEveryConnection` |
| 节点证明绑定与签名链独立性 | `TestAttestationBinding`、`TestNodeAttestationCannotReplaceSignatureChain` |
