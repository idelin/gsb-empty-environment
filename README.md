# WS-Security Timestamp 校验

`netsec` 是一个无第三方依赖、可导入的 Go 包，用于校验 SOAP 1.1/1.2
报文中的 WS-Security 1.1 `Security` 头。它负责结构与时间校验、签名引用
覆盖检查，以及 UsernameToken 重放防护；实际 XML 签名密码学校验由调用方注入。

## 校验规则

- `Security` 头中恰有一个 `Timestamp`，且 `Timestamp` 必须是 `Security` 的直接子元素。
- `Created` 最多可比当前时间超前 60 秒；`Expires` 必须晚于 `Created`、间隔不超过 300 秒，且不能已过期。
- `Timestamp` 与 SOAP `Body` 必须各自恰有一个 `wsu:Id`，所有 `wsu:Id` 值在文档中必须唯一。
- 注入的签名校验器必须返回已签名引用的 `wsu:Id` 集合；`Timestamp` 和 `Body` 的 ID 都必须在该集合中。
- 默认要求恰有一个 `UsernameToken`。其 `Nonce` 与 `Created` 组合在一次窗口内只能使用一次。

## 用法

```go
validator := netsec.NewValidator(func(xmlDoc []byte) ([]string, error) {
    // 在这里完成 XMLDSig 的规范化和密码学校验。
    // 返回 SignedInfo 中所有引用指向的 wsu:Id，例如 []string{"#TS-1", "#BODY-1"}。
    return signedIDs, verifyXMLSignature(xmlDoc)
})

result, err := validator.Validate(soapBody)
if err != nil {
    // 拒绝请求
}
_ = result
```

`Validator` 应作为网关级对象复用；默认的内存重放存储跨请求生效。多实例部署时，
通过 `WithReplayStore` 注入共享存储，并保证 `CheckAndStore` 的原子性。

若网关还接受没有 UsernameToken 的其他认证方式，可显式使用
`netsec.WithOptionalUsernameToken()`；只要存在 UsernameToken，仍会执行完整校验和重放检查。
