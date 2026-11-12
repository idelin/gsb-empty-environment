# SealedSecret作用域与注解篡改

Sealed Secrets 用控制器公钥加密 Secret，作用域有 strict（命名空间与名称都进入加密标签）、namespace-wide（只含命名空间）与 cluster-wide（无标签），攻击者拿到别的命名空间的密文后改名重放，或把 strict 的密文改注解成 cluster-wide。为 Go 控制器写供解封印调用的作用域校验包：输入 SealedSecret（命名空间、名称、注解 sealedsecrets.bitnami.com/cluster-wide 与 namespace-wide、加密数据项）、解密所用的标签及解密结果；范围由注解决定，两个注解同时为真拒绝；解封印所用标签须与当前对象推导出的一致才能成功（strict 为命名空间加名称，namespace-wide 为命名空间，cluster-wide 为空）；注解在加密后被篡改不得放宽范围；多个控制器密钥轮换时逐个尝试，失败原因不得区分是标签错误还是密文错误。用改名重放、注解放宽、双注解和合规解封印验证。
