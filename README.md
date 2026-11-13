# kb-jwt绑定披露

持有者证明换一套披露后仍能附在原来的kb-jwt上。kb-jwt的typ必须是kb+jwt，sd_hash等于规范化披露字节的SHA-256，cnf里的jkt等于证明密钥。披露不变且jkt匹配的证明接受。可导入的Go库供验证组件调用。增删一条披露后再用原证明。
