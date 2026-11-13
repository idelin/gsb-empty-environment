# 私有访问令牌挑战来源核对

PrivateToken 挑战被嵌入页转发到别的来源，攻击者借此换取可跨站复用的令牌。为TypeScript浏览器网络栈写可导入的挑战处理包（RFC 9577）：解析 token_type、issuer_name、redemption_context（0 或 32 字节）、origin_info；origin_info 非空时请求来源须在其中，为空时只限顶层同源；重编码须与原字节一致，并输出 challenge_digest。
