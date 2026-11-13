# 展示受众限制

出示给A的kb-jwt被转交给B的验证方。kb-jwt的aud必须等于本次验证方标识，nonce等于本次挑战；不符则整份出示无效。aud和nonce都对的出示接受。由钱包调用可导入的TypeScript库。保持签名，只替换aud。
