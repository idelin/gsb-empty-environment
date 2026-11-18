# DANE TLSA选择器匹配

邮件投递代理靠TLSA绑定服务器证书，选择器或匹配类型错位会让错误证书与记录相等。为Go投递组件实现DANE-EE与DANE-TA校验包，供TLS握手后调用（TLSA已通过DNSSEC验证）：selector 0比较整证书、1比较SPKI，matching type 0、1、2分别比较原文、SHA-256、SHA-512；DANE-EE不查名称与有效期，DANE-TA要求链上有匹配证书并只接受能链到该TA的路径；同一名字多条可用TLSA记录时任一命中即通过，全部不命中才拒绝；selector与matching错位必须拒绝而不能退化成字节比较。用异选择器同字节、TA旁路、多记录部分命中和合法证书验证。
