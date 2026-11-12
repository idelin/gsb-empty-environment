# ECH接受确认值校验

客户端判断服务端是否接受了 ECH，只能依据 ServerHello.random 末尾 8 字节的确认值，攻击者若能伪造接受信号，客户端会把外层握手当成内层握手。为Go TLS 客户端写供 ServerHello 处理调用的确认值校验包，按本题约定计算：确认值 = HKDF-Expand-Label(HKDF-Extract(全零, ClientHelloInner.random), "ech accept confirmation", Hash(transcript), 8)，transcript 为 ClientHelloInner 与把 random 末 8 字节置零的 ServerHello 的拼接，哈希为 SHA-256；HelloRetryRequest 之后改用标签 "hrr ech accept confirmation"，transcript 取置零后的 HRR，确认值位于 HRR 的 ECH 扩展末 8 字节；用常量时间比较，不相等即视为被拒绝；被拒绝时已算出的内层密钥必须丢弃，且不得采用内层参数。用匹配、末字节翻转、HRR 用错标签、置零偏差验证。
