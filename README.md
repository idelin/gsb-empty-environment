# Transit密文版本绑定

KMS 的 Transit 引擎密文带 vault:vN: 版本前缀，攻击者篡改前缀让服务用已弃用的旧密钥解密，或把一个密钥的密文拿到另一个密钥名下。为Go密钥服务实现供加解密接口调用的版本绑定包：密文格式 vault:v<版本>:<base64>，AEAD 的附加数据绑定密钥名与版本，不能仅靠前缀推断；低于 min_decryption_version 的版本拒绝解密，min_encryption_version 约束新加密所用的最低版本；rewrap 在目标版本重新加密且不暴露明文；收敛加密的上下文须参与子密钥派生。用改前缀、换密钥名、低于最低解密版本、rewrap 和合法解密验证。
