# CloudEvents投递授权握手

事件系统向订阅者的 Webhook 投递之前，须确认对方愿意接收，攻击者把受害服务的地址登记成订阅者，让平台变成放大流量的跳板。为Go的事件分发服务写可导入的订阅验证包（CloudEvents HTTP Webhook 滥用防护）：登记目标时先发 OPTIONS，带 WebHook-Request-Origin 与 WebHook-Request-Rate；目标响应须带 WebHook-Allowed-Origin（值为发送方 origin 或 `*`）和 WebHook-Allowed-Rate（正整数或 `*`），缺任一项拒绝登记；实际速率取两方较小值；回调地址须为 HTTPS，主机解析出回环或私网地址拒绝，重定向不跟随；登记 24 小时未重新验证则暂停投递；验证响应来自与登记地址不同的主机时拒绝。测缺头、速率取小、内网地址、重定向、过期。
