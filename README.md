# TURN凭据与对端约束

攻击者可篡改TURN用户名里的过期时间或租户段，也可请求中继到内网。为Go TURN鉴权组件实现可导入的凭据校验包：用户名为expiry:tenant:user（user无冒号），MESSAGE-INTEGRITY密钥取MD5(username:realm:password)，password由共享密钥HMAC派生；对端不得是回环或私网。用STUN包测试。
