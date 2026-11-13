# 洋葱描述符签名链与计数回滚

攻击者向 Tor v3 HSDir 上传伪造或回滚的洋葱服务描述符。为Go中继写可导入的描述符准入包：hs-descriptor 版本须为 3，签名证书须由当前时段的 blinded key 签发且未过期，签名覆盖固定前缀加正文，revision-counter 须大于缓存值；验签由调用方注入。用改版本、证书过期、改正文、counter 回滚验证。
