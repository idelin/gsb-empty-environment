# SAML响应的请求ID与断言重放

IdP 签名的响应被截获后可在另一会话里重放，或冒充由本服务发起的登录。为Go服务提供方写可导入的响应关联包：发出 AuthnRequest 时登记 ID 与过期时间；响应的 InResponseTo 须命中未消费的登记项并消费一次；无 InResponseTo 的 IdP 发起登录只在配置允许时接受；断言 ID 已见则拒绝；Recipient 须等于 ACS 地址，NotOnOrAfter 允许 60 秒时钟偏差。用重放、错 ID、过期登记和 IdP 发起验证。
