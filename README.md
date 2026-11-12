# HSTS策略解析与子域匹配

攻击者经明文 HTTP 注入 Strict-Transport-Security 头，或在子域发 max-age=0 想解除父域保护。为TypeScript浏览器网络栈写可导入的 HSTS 存储：只处理 HTTPS 响应里的头，IP 字面量主机忽略；max-age 须为十进制整数，指令名不分大小写，重复指令使整头无效，max-age=0 删除该条目；includeSubDomains 覆盖子域，子域的删除只影响自身；按标签边界匹配。测明文注入、重复指令、仿冒后缀。
