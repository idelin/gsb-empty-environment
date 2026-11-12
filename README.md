# NATS leafnode JWT签发链

攻击者用未列入账户的签名密钥签发用户 JWT，或在 issuer_account 冒充别的账户接入 leafnode。为Go服务器写可导入的 leafnode 认证包：账户 JWT 由运营商密钥或其登记的签名密钥签发；用户 JWT 由账户标识密钥或其 signing_keys 内的密钥签发，后者须带等于该账户的 issuer_account；nonce 签名用用户公钥验证；iat 不晚于账户撤销表中该用户撤销时间即无效；过期拒绝。用 Ed25519 真实签名测未登记密钥、冒充、撤销边界、nonce 重放。
