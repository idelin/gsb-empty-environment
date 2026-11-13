# Client Hints委托与降级

第三方 iframe 若能拿到高熵 Client Hints，就能做设备指纹，攻击者会靠伪造的 Accept-CH 或被放宽的 Permissions-Policy 扩大发送范围。为TypeScript浏览器网络栈写供请求发出前调用的 Client Hints 附加包：顶层来源用 Accept-CH 声明想接收的提示；低熵提示（Sec-CH-UA、Sec-CH-UA-Mobile、Sec-CH-UA-Platform）默认发给同源请求；高熵提示（如 Sec-CH-UA-Full-Version-List、Sec-CH-UA-Model）只发给同源，或发给 Permissions-Policy 中对应 ch-ua-* 特性的列表明确包含目标来源的跨源请求（列表为 * 时放行任意来源）；Accept-CH 只在 HTTPS 的顶层导航响应上生效，子资源响应与 HTTP 响应里的声明忽略。用无委托的跨源请求、列表含目标来源、明文页面声明、子资源声明和 * 列表验证。
