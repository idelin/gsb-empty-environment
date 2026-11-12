# REQUIRETLS逐跳降级拒绝

邮件带 REQUIRETLS 要求全程加密，攻击者可剥离 STARTTLS、在明文连接上伪造该参数，或让中继在下一跳降级。给Go邮件中继写供出站投递调用的组件，按 RFC 8689：只有在已建立 TLS 的入站会话里才承认 MAIL FROM 的 REQUIRETLS 参数，明文会话里出现则拒绝该命令；出站时下一跳须在 STARTTLS 之后的 EHLO 里声明 REQUIRETLS，且证书经调用方注入的校验通过，任一条件不满足就把邮件作为永久失败退回，不降级也不换其他 MX 明文重试；邮件头里的 TLS-Required: No 只能由发件人在不带 REQUIRETLS 时使用。用明文伪造参数、剥离 STARTTLS、证书校验失败和合法链路验证。
