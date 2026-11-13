# 一键退订POST令牌绑定

邮件安全扫描器会预取邮件里的链接，攻击者也能伪造退订地址替别人退订。为TypeScript邮件发送系统写可导入的一键退订校验包（RFC 8058）：请求须为 POST，正文须恰为 List-Unsubscribe=One-Click（application/x-www-form-urlencoded）；URL 里的令牌为 HMAC-SHA256(密钥, 收件人地址 + 0x00 + 列表 ID + 0x00 + 过期秒的十进制文本) 的十六进制，并同时带过期秒，用常量时间比较；GET 请求不得改变订阅状态，只返回确认页；令牌与路径中的列表 ID 不符、已过期或收件人被篡改时拒绝；出站邮件 DKIM 签名的头集合必须覆盖 List-Unsubscribe 与 List-Unsubscribe-Post，否则发送前拒绝构造该邮件。用 GET 预取、令牌换列表、过期和未签名头验证。
