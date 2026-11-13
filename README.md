# 派生密钥盐前缀与凭据映射

攻击者页面想借 WebAuthn 的 prf 扩展，用自选盐复现别的协议的 hmac-secret 输出，或让 evalByCredential 指向无关凭据。为TypeScript浏览器写可导入的校验包：盐先算 SHA-256("WebAuthn PRF" ‖ 0x00 ‖ 盐)；evalByCredential 的键须为 base64url 且在 allowCredentials 内，后者为空则拒绝带该成员；缺 first 拒绝。
