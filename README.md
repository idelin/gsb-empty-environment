# TLS握手消息顺序与提前ChangeCipherSpec

密钥交换完成前塞入 ChangeCipherSpec，或跳过 Certificate，客户端可能用空主密钥或未认证的密钥继续。为Go TLS 1.2 客户端写可导入的握手状态机包：按 ServerHello、Certificate、ServerKeyExchange（仅 ECDHE）、可选 CertificateRequest、ServerHelloDone、CCS、Finished 接收，CCS 只能在本端发出 ClientKeyExchange 之后出现；乱序、重复、缺失一律以 unexpected_message 中止且不启用加密。用提前 CCS、缺 Certificate 和重复 ServerHello 验证。
