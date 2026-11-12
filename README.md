# RADIUS Message-Authenticator强制

针对RADIUS的碰撞类攻击会伪造Access-Accept，请求和响应都需可验证的Message-Authenticator。为Go RADIUS服务端实现报文校验包，供UDP接收循环调用：Access-Request中它须为首个属性并按HMAC-MD5验证，缺失、重复或长度非16字节拒绝；响应用请求认证符计算，客户端丢弃缺失或不匹配的响应。用被篡改属性、缺字段和合法交换验证。
