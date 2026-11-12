# Reporting-Endpoints投递授权

文档通过 Reporting-Endpoints 声明报告端点，攻击者利用响应头注入或第三方页面声明指向内网地址的端点，让浏览器代他发送 POST。为TypeScript浏览器网络栈写可导入的报告投递包（Reporting API 与 RFC 8941 字典）：头值按 Structured Fields 字典解析，成员值须是字符串，解析失败则整个头忽略；相对 URL 以响应 URL 解析，结果须为 HTTPS 且不含用户信息；主机为回环、私网、链路本地地址（含 IPv4 映射的 IPv6、十进制与十六进制整数写法）时，除非文档自身来自同一地址空间，一律不投递；投递请求不带 Cookie 与 Authorization，Content-Type 为 application/reports+json，单次数组不超过 100 条；报告引用未声明的端点名时丢弃。测内网端点、整数 IP 写法、字典解析错误、凭据携带。
