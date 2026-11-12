# DSSE供应链身份绑定

制品准入服务验证DSSE声明时，攻击者可把有效签名换绑到另一载荷类型、仓库或构建身份。实现供Go准入宿主调用的验证包：按DSSE PAE精确构造签名输入，拒绝重复签名者和未知payloadType；解码in-toto Statement后要求subject摘要恰有目标制品，predicate中的仓库、提交和构建者身份与调用方策略匹配。签名证书链须在给定时刻有效，证书身份只能从指定扩展读取。用同载荷换类型、额外subject、同名仓库不同身份和合法声明验证，仅完整绑定时调用发布回调。

## 使用方式

```go
policy := netsec.Policy{
    TargetName:   "artifact.tar.gz",
    TargetDigest: map[string]string{"sha256": artifactSHA256},
    Repository:   "github.com/acme/app",
    Commit:       sourceCommit,
    Builder:      "https://builders.example/acme",
    Identity:     "https://builders.example/acme",
    IdentityOID:  signerIdentityOID,
    Roots:        trustedRoots,
    Time:         admissionTime,
    Publish: func(statement *netsec.VerifiedStatement) error {
        return publish(statement)
    },
}

// The chain is ordered leaf first, then any intermediates.
statement, err := netsec.VerifyEnvelope(envelope, certificateChain, policy)
```

验证顺序固定为：DSSE 载荷类型与签名 → X.509 链及指定扩展中的身份 → in-toto
唯一 subject 和目标摘要 → predicate 的仓库、提交和构建者 → 发布回调。任一步失败都会
返回错误，且不会调用回调。
