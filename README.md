# WebAuthn关联来源判定

多个品牌域共用一个RP ID，攻击者会用相似域名或被劫持的子域发起断言。
`webauthn` 子包为断言（`authenticatorGetAssertion`）校验提供独立的来源与 RP ID
判定逻辑，仅依赖 Go 标准库。

## 判定规则

- `/.well-known/webauthn` 清单的 `origins` 至多包含 5 个完整来源
  （`scheme://host[:port]`），解析时做来源规范化与去重；空清单、超额、重复或
  非法来源一律拒绝。
- 断言的 `clientData.origin` 必须与清单条目**精确相等**（默认端口归一化、
  IPv6 保留方括号），不做后缀匹配，因此相似域、未登记子域和异端口均被拦截。
- `authenticatorData` 前 32 字节必须等于 `SHA-256(RP ID)`，使用恒定时间比较。
- `crossOrigin == true` 或携带 `topOrigin` 时（含同源嵌套框架），顶层页来源
  也必须在清单内；缺少 `topOrigin` 或顶层页不在清单内则拒绝。
- HTTP 来源仅允许回环主机（`localhost`、`127.0.0.1`、`::1`），生产环境使用 HTTPS。

## 用法

```go
manifest, err := webauthn.FetchManifest(ctx, http.DefaultClient, rpID)
// 或对静态配置使用 webauthn.ParseManifest(data)

verifier, err := webauthn.NewVerifier(rpID, manifest)
if err != nil { // 非法 RP ID 或清单
}

clientData, err := webauthn.ParseClientData(assertion.Response.ClientDataJSON)
// 其余 clientData（type/challenge）与签名校验由调用方完成

err = verifier.VerifyAssertion(webauthn.AssertionRequest{
    ClientData:        clientData,
    AuthenticatorData: authenticatorData,
})
```

拒绝原因通过哨兵错误区分，可用 `errors.Is` 匹配：
`ErrManifest`、`ErrOriginNotAllowed`、`ErrTopOriginRequired`、`ErrRPIDHash`、
`ErrRPID`。

## 测试

`go test ./...` 覆盖相似域（`examp1e-brand.com`、后缀仿冒）、被劫持但未登记的
子域、异端口、跨域/同源嵌套框架（含缺失或恶意 `topOrigin`）、伪造 `rpIdHash`
以及合法断言放行，另含清单抓取（状态码、Content-Type、体积上限、传输错误）的
端到端流程测试。
