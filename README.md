# Signed Exchange签名窗口与证书资格

Signed HTTP Exchange 让缓存方以原站身份分发内容，攻击者拿到长期签名，或把别的域的证书拼进交换包，就能冒充原站。为Go分发节点写可导入的 SXG 签名元数据判定包：签名算法为 ecdsa，证书叶子须带 CanSignHttpExchanges 扩展且有效期不超过 90 天；签名的 date 与 expires 之差不超过 604800 秒，当前时间须落在 [date, expires) 内；validity-url 须与 request-url 同源；cert-sha256 须等于调用方提供的叶子证书 DER 的 SHA-256 的 base64；request-url 的主机须被证书 SAN 覆盖。用窗口 8 天、跨源 validity-url、证书缺扩展、哈希不符、SAN 不覆盖和 [date, expires) 边界验证。
