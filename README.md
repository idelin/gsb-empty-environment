# DANE TLSA选择器匹配

邮件投递代理靠TLSA绑定服务器证书，选择器或匹配类型错位会让错误证书与记录相等。为Go投递组件实现DANE-EE与DANE-TA校验包，供TLS握手后调用（TLSA已通过DNSSEC验证）：selector 0比较整证书、1比较SPKI，matching type 0、1、2比较原文、SHA-256、SHA-512；DANE-EE不查名称与有效期，DANE-TA要求链上有匹配证书。用异选择器同字节验证。
