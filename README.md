# DDS权限规则判定

攻击者的DDS参与者持有有效身份证书，却想向未许可的主题发布，或用通配符绕过拒绝规则。给Go写供访问控制插件调用的权限判定包：已验签权限文档的规则按顺序匹配，首条命中的allow或deny生效，无命中拒绝；主题用区分大小写的fnmatch式通配，域ID与分区也要匹配；有效期外、主体名与证书主题不符的文档整体无效。用先deny后allow、通配越权和过期文档验证。

## 设计要点

- 签名校验在插件层完成，本包只消费已验签的 `PermissionDocument`。
- 规则按文档与规则顺序求值，首条命中的 `Allow`/`Deny` 生效；无命中默认 `Deny`（fail-closed）。
- 主题与分区使用区分大小写的 fnmatch 通配（`*`、`?`、`[...]`/`[!...]`、`\` 转义），域 ID 为精确整数匹配。
- 文档不在 `[NotBefore, NotAfter)` 有效期内，或 `SubjectName` 与证书主题不一致时，整篇文档失效并跳过。

## 用法

```go
engine, err := netsec.NewEngine([]netsec.PermissionDocument{
    {
        SubjectName: "cn=pub",
        NotBefore:   issuedAt,
        NotAfter:    expiresAt,
        Rules: []netsec.Rule{
            {Effect: netsec.Deny, Action: netsec.Publish,
                Topics: []string{"secret/*"}, DomainIDs: []int{0}},
            {Effect: netsec.Allow, Action: netsec.Publish,
                Topics: []string{"*"}, DomainIDs: []int{0}},
        },
    },
})
if err != nil {
    return err
}

decision, _ := engine.Authorize(netsec.AccessRequest{
    CertificateSubject: "cn=pub",
    Action:             netsec.Publish,
    Topic:              "secret/keys",
    DomainID:           0,
    Partition:          "factory-a/line1",
})
// decision == netsec.Deny：前置 deny 优先于通配 allow
```

测试：`go test ./...`
