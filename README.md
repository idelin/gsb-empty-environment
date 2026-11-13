# SAML响应的请求ID与断言重放

IdP 签名的响应被截获后可在另一会话里重放，或冒充由本服务发起的登录。为Go服务提供方写可导入的响应关联包：发出 AuthnRequest 时登记 ID 与过期时间；响应的 InResponseTo 须命中未消费的登记项并消费一次；无 InResponseTo 的 IdP 发起登录只在配置允许时接受；断言 ID 已见则拒绝；Recipient 须等于 ACS 地址，NotOnOrAfter 允许 60 秒时钟偏差。用重放、错 ID、过期登记和 IdP 发起验证。

## 使用

```go
correlator, err := netsec.New(netsec.Config{
    ACSURL:            "https://sp.example.org/saml/acs",
    AllowIdPInitiated: false, // 默认拒绝无 InResponseTo 的响应
})
if err != nil {
    return err
}

// 发出 AuthnRequest 前生成并登记 ID；5 分钟后登记失效。
pending, err := correlator.BeginAuthnRequest(5 * time.Minute)
// 将 pending.ID 写入 AuthnRequest 的 ID 属性。

// 先完成 IdP 签名校验，再做响应关联与重放校验。
validated, err := correlator.ValidateResponse(decodedXML)
if err != nil {
    return err
}
```

已有自行生成的请求 ID 时，可调用 `RegisterRequest(id, expiresAt)` 登记；测试需要固定时钟时使用 `ValidateResponseAt(xml, now)`。

校验顺序与规则：

- 有 `InResponseTo`：必须命中未过期的登记项；登记项在首次成功响应时原子消费，后续使用返回 `ErrRequestReplayed`。
- 无 `InResponseTo`：只有 `AllowIdPInitiated` 为真时才接受。
- 每个断言 ID（按 Issuer + ID）只接受一次；跨会话重放返回 `ErrAssertionReplayed`。
- `SubjectConfirmationData.Recipient` 必须与 `ACSURL` 完全一致。
- `NotBefore`、`NotOnOrAfter` 允许 60 秒偏差；可用 `Config.ClockSkew` 调整。

包不负责 XML 签名验证；调用方必须先验证 IdP 签名，避免接受伪造响应。状态保存在进程内，多实例部署时应将登记项与已见断言 ID 放入共享存储。
