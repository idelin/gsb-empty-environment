# ECH GREASE扩展解析

ECH 客户端在不确定服务端支持时会发送 GREASE 扩展，服务端对它与真实 ECH 扩展处理不一致时，攻击者可借畸形扩展让握手失败，或探测服务端是否支持 ECH。为Go TLS 服务端写供 ClientHello 解析调用的 ECH 扩展校验包：扩展（0xfe0d）首字节为类型，outer（0）后接 KDF 与 AEAD 编号各 2 字节、config_id 1 字节、enc（2 字节长度前缀）和 payload（2 字节长度前缀）；第一个 ClientHello 中 enc 长度须等于已配置 KEM 的封装密钥长度（X25519 为 32），否则 decode_error；HelloRetryRequest 之后的第二个 ClientHello 中 enc 须为空；inner（1）只能出现在内层，出现在外层按 illegal_parameter；config_id 不属于任何已配置项时不解密，按未用 ECH 继续握手并在 EncryptedExtensions 给出 retry_configs；扩展重复出现按 illegal_parameter。用 enc 长度 31、外层携带 inner、未知 config_id、重复扩展、第二个 ClientHello 非空 enc 和合法 GREASE 验证。
