# CAA检查漏掉了上级域名

证书颁发机构只查`www.example.com`的CAA记录，而策略写在`example.com`，攻击者在其控制的子域下获得签发，绕过父域限制。请实现供Go证书颁发服务调用的可导入CAA判定组件。按RFC 8659从完整域名向根逐级查询，遇到第一个非空CAA集合即停止并使用它；issue、issuewild、iodef按标签解析，issuewild只影响通配请求，未知标签忽略并记录。CNAME与委托使查询链变化时重新判定，DNSSEC验证状态与解析失败按策略处理，不得因超时默认放行。用父域限制、通配证书、CAA空集、CNAME委托、DNSSEC失败和合法子域授权验证只在授权范围内签发。

## 实现

`netsec` 包提供：

- RFC 8659 `RelevantCAASet` 判定：从完整域名开始，每级追随 CNAME/DNAME，空 RRset 继续向父域上溯；别名目标无 CAA 时仍从原域名的父域上溯，遇到第一个非空集合立即停止。
- `issue` 与 `issuewild` 的签发者匹配、参数策略、空 issuer/格式错误处理；`iodef` 解析和未知标签记录。
- 未知 critical 标签按 RFC 8659 拒绝签发；非 critical 未知标签忽略并写入 `slog`。
- 可注入的 `Resolver` 接口，以及可选的 `ReferralResolver` 委托续查接口；无法续查的 referral 失败关闭。
- DNSSEC 策略：bogus 永远拒绝；可选择接受 insecure、拒绝未知状态或强制 secure。
- 所有 DNS 错误（包括超时、SERVFAIL、NXDOMAIN）均拒绝签发，不存在超时放行路径。
- `Checker.Authorize` 提供仅供签发路径使用的 error-only 接口；`Checker.Check` 同时返回
  `Decision`，用于审计拒绝原因。

## 用法

```go
resolver := &netsec.DNSResolver{
    Servers:    []string{"127.0.0.1:53"}, // 应指向 DNSSEC 验证递归解析器
    Validating: true,
}
checker := netsec.NewChecker(
    resolver,
    netsec.Policy{
        IssuerDomainNames: []string{"ca.example"},
        DNSSECMode:        netsec.DNSSECAcceptInsecure,
    },
)

if err := checker.Authorize(ctx, netsec.Request{
    Name:     "*.example.com",
    Wildcard: true,
}); err != nil {
    // Do not issue. 错误可能是查询/配置失败，也可能是 CAA 策略拒绝。
    return err
}
```

`Request.Name` 也可以直接写成 `*.example.com`，组件会识别通配符。证书服务应把
`Authorize` 返回的任何错误都视为拒绝。若需要记录拒绝原因，可调用 `Check` 并检查
`Decision.Allowed` 与 `Decision.Reason`。
