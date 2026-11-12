# Ingress路径规范化与前缀边界

边缘代理按 Ingress 的 Exact 与 Prefix 路径规则选后端，攻击者发 /admin/../public、//admin、/admin%2Fx 或 /ADMIN 绕过只给内部服务的 /admin 前缀。为Go代理实现可嵌入的路径匹配包：匹配与转发使用同一份规范路径，Prefix 按元素边界匹配（/foo 命中 /foo/bar 不命中 /foobar），点段先消去再匹配，含 %2F、%5C 或解码后含 NUL 的请求拒绝，大小写敏感，多条规则命中时 Exact 优先、再取最长 Prefix。用上述路径验证，并断言转发路径与匹配路径一致。
