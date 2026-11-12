# SCRAM通道绑定降级拒绝

SCRAM-SHA-256-PLUS 把认证绑定到 TLS 通道，攻击者在中间剥掉 PLUS 机制，让客户端回落到不带绑定的 SCRAM。为Go认证服务端写可导入的 SCRAM 通道绑定判定包，供第一条客户端消息处理调用：gs2 头首段为 n（客户端不支持绑定）、y（客户端支持但以为服务端不支持）或 p=类型（要求绑定）；服务端通告了 -PLUS 机制而客户端发 y 视为降级，拒绝；机制为 -PLUS 而 gs2 为 n 或 y 拒绝；机制不带 -PLUS 而 gs2 为 p= 拒绝；p= 的类型须在服务端支持集合内（tls-server-end-point、tls-exporter）；最终消息的 c= 值解码后须等于 gs2 头加绑定数据，绑定数据由调用方按类型提供，不符拒绝；tls-server-end-point 的数据为证书 DER 的哈希，哈希算法取证书签名算法所用者，MD5 与 SHA-1 升级为 SHA-256。用剥离 PLUS 后的 y、机制与 gs2 不一致、未知绑定类型、c= 不符和 SHA-1 证书验证。
