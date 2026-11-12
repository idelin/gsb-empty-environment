# eSIM下载会话认证防重放

攻击者重放旧的eUICC认证应答，或拿别人订单的matchingId换eSIM配置文件。给Go的SM-DP+写供ES9+会话调用的守卫：transactionId与服务端挑战只能用一次并随会话过期；应答签名须覆盖二者；matchingId消费后作废并绑定EID；确认码哈希=SHA256(SHA256(确认码)‖transactionId)，错误超3次锁定订单。用重放应答、串订单和确认码穷举验证。
