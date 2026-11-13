# 密钥属性互斥与封装信任

HSM 若允许同一把密钥既能封装又能解密，攻击者可让它封装敏感密钥再解密得到明文。为Go的 PKCS#11 访问层写可导入的属性策略包：同一密钥不得同时有 CKA_WRAP 与 CKA_DECRYPT（UNWRAP 与 ENCRYPT 同理）；CKA_SENSITIVE 变真后不可改回，CKA_EXTRACTABLE 变假后不可改回；被封装密钥带 CKA_WRAP_WITH_TRUSTED 时，封装密钥须带 CKA_TRUSTED。
