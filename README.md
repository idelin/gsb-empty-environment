# 内容提供方路径权限与点段穿越

提供方给 /public/ 前缀放宽了读权限，攻击者请求 content://auth/public/..%2Fprivate/db 绕到私有路径。为Go应用静态检查包写可导入的 path-permission 求值：匹配前先把百分号编码解码到不动点，再按点段规范化；取所有匹配规范化路径的规则，任一要求更高权限或 deny 的项生效；grant-uri-permission 的前缀同样对规范化路径判定；规范化后越出 provider 根的请求拒绝。用 ..%2F、双重编码、同名前缀和合法路径验证。
