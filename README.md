# LDAP 引用跟随组件

`example.org/netsec/q0241` 是一个可导入的 Go 包，用于企业目录网关在用户搜索时安全地跟随 LDAP referral。它把“是否联系引用主机”和“是否发送绑定口令”拆成两个独立决策：只有引用主机位于显式信任域、端口属于该协议的允许列表、且配置允许转发凭据时，才会重新发送简单绑定口令；其他可联系的目标只能使用空 DN/空口令的匿名简单绑定，或者直接标记为不可跟随。

## 快速使用

```go
audit := &netsec.MemoryAudit{}

f, err := netsec.NewFollower(netsec.Config{
    TrustedDomains:            []string{"corp.example.com"},
    AllowedPorts: map[netsec.Scheme][]int{
        netsec.SchemeLDAP:  {389},
        netsec.SchemeLDAPS: {636},
    },
    AllowCredentialForwarding: true,
    AllowAnonymousFollow:      true,
    MaxReferralDepth:          4,
    Forest:                    "corp",
    DomainForests: map[string]string{
        "corp.example.com":    "corp",
        "partner.example.com": "partner",
    },
    Audit: audit,
})
if err != nil {
    return err
}

result, err := f.Follow(ctx, netsec.Request{
    Server:     "ldaps://dc1.corp.example.com/ou=users,dc=corp,dc=example,dc=com",
    BindDN:     "cn=gateway,ou=services,dc=corp,dc=example,dc=com",
    Password:   bindPassword,
    Filter:     "(uid=alice)",
    Attributes: []string{"uid", "cn", "mail"},
    Source:     clientAddr, // 原始请求来源，写入每条审计事件
})
```

初始服务器与 referral 使用同一套策略。如果初始主机不在信任域内，调用方只会得到匿名连接或明确的不可跟随错误，不会把口令发送出去。

## 安全规则

- **URL 解析**：解析 scheme、主机、端口、DN、属性、scope、filter 和扩展字段。拒绝 userinfo、fragment、opaque URL、非法端口和非 `ldap`/`ldaps` 协议。
- **信任域匹配**：按规范化后的字面主机名做精确匹配或标签后缀匹配，不做 DNS 解析。`trusted.example.com` 不会匹配 `trusted.example.com.attacker.test`；大小写和末尾点会被规范化。非 ASCII 主机名会被拒绝，避免在未做 IDNA 处理时产生歧义。
- **端口限制**：每个 scheme 有独立的允许端口，默认只允许 `ldap:389` 和 `ldaps:636`。同一主机换成其他端口不会获得凭据。
- **凭据转发**：必须同时满足“主机受信”“端口允许”“`AllowCredentialForwarding=true`”。否则在 `AllowAnonymousFollow=true` 时使用空 DN、空口令的匿名简单绑定，否则返回 `DoNotFollow`。
- **TLS**：`ldaps://` 从连接建立即启用 TLS；`ldap://` 默认先执行 StartTLS 再绑定。两者都使用目标主机名作为 `ServerName` 做证书链和主机身份校验。StartTLS 失败不会回退到明文；从 LDAPS/StartTLS 引用到明文会被拒绝。`InsecureSkipVerify` 会被配置校验拒绝。
- **链限制**：显式深度上限、按规范化目标（scheme/host/port/DN）检测循环、按 `DomainForests` 最长后缀匹配森林边界。跨森林默认拒绝，只有 `AllowCrossForest=true` 才放行。
- **审计**：每次策略决策与绑定都会记录原始请求来源、引用来源、目标、深度、森林、绑定 DN、认证机制（`simple` 或 `anonymous-simple`）、传输模式和是否转发了凭据。审计结构中没有口令字段。

## 结果处理

`FollowResult.Hops` 列出实际联系过的主机。被策略拒绝的引用会进入 `Unfollowed`，其中包含原始 URL、`Decision` 和错误原因，例如：

- `ErrUntrustedHost`
- `ErrPortNotAllowed`
- `ErrCleartextDowngrade`
- `ErrReferralLoop`
- `ErrMaxReferralDepth`
- `ErrCrossForest`

`Follower.Evaluate` / `EvaluateReferral` 可以只做策略判断而不发起连接，便于网关做预检或审计。

## 可替换传输

`Config.Dialer` 接受实现 `Dial(context.Context, Target) (Conn, error)` 的传输。跟随器在调用 `Dial` 前已经完成所有信任、端口、深度、循环、森林和降级检查；`Bind` 只会收到调用方口令（`FollowWithCredentials`）或空字符串（`FollowAnonymous`）。

自定义 `Dialer` 必须遵守 `Target.Mode`：`TLSModeLDAPS`/`TLSModeStartTLS` 必须在返回连接前完成证书和主机身份校验，禁止在失败后回退明文。

## 验证

```sh
go test ./...
go vet ./...
```

测试覆盖合法同域引用、跨域引用、主机别名后缀、端口替换、循环引用、深度上限、跨森林边界、明文降级、匿名可跟随，以及 LDAPS/StartTLS 的证书与主机身份校验；对每个场景都断言口令只出现在受信主机的绑定调用中。
