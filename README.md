# DANE TLSA选择器匹配

邮件投递代理靠 TLSA 绑定服务器证书，选择器或匹配类型错位会让错误证书与记录相等。为 Go 投递组件实现 DANE-EE 与 DANE-TA 校验包，供 TLS 握手后调用（TLSA 已过 DNSSEC 验证）：只处理 usage 3 与 2，其他 usage 报不支持而不是退回默认；selector 0 比较整证书、1 比较 SPKI，matching 0、1、2 分别比较原文、SHA-256、SHA-512；DANE-EE 不看名称与有效期，DANE-TA 要求链上存在能连到该 TA 的路径并只采用该路径；同名多条可用记录任一命中即通过，全部不命中才拒绝，并逐条返回失败原因（usage 不支持、selector 与 matching 错位、摘要不符、链不达）。用异选择器同字节、TA 旁路、部分命中与错位摘要对照各自原因。
