# WebFinger与host-meta主机一致性

WebFinger 与 host-meta 的响应由目标主机自己提供，攻击者控制的主机可用重定向、模板或 subject 把查询引向别的主机，冒充另一域名下的账号。给TypeScript联邦客户端写供账号解析调用的校验包，按 RFC 7033：只请求 https，查询主机取自 acct 的域名；重定向仅在同一主机内跟随，最多两次；JRD 的 subject 须是 acct: 且域名与查询域名相同（大小写不敏感），否则整条丢弃；host-meta 中 lrdd 的 template 必须含 {uri}、是 https 且主机与查询主机相同；rel 为 self 的链接 href 主机若与查询主机不同，只在调用方白名单内才采纳。用跨主机重定向、subject 域名不符、外域 lrdd 模板、自链接外域验证。
