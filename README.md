# Impersonate头授权求值

Kubernetes 风格的 API 服务器允许持有 impersonate 权限的主体用 Impersonate-User 与 Impersonate-Group 头代为请求，攻击者借此提升身份，例如只拿到模拟某个用户的权限，却附加 system:masters。为Go的 API 服务器写可导入的模拟授权包：被模拟的每个对象（用户、组、serviceaccount、uid、extra 键）都是一次独立授权检查，动词为 impersonate，资源为 users、groups、serviceaccounts、uids、userextras/<键>，resourceNames 可限定；Impersonate-Group 出现而没有 Impersonate-User 拒绝；Impersonate-Extra-<键> 的键须百分号解码并转小写后再检查；任何一项不被允许则整个请求 403；通过后审计同时记录原身份与被模拟身份，之后的授权只使用被模拟身份，原身份的权限不叠加。测只授权用户却附加组、extra 键编码绕过、无 User 的 Group、权限叠加。
