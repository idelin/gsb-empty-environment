# 实例元数据代理IMDSv2守卫

实例元数据服务被服务端请求伪造打到后会交出角色凭证，攻击者让应用代发对 169.254.169.254 的请求。为Go节点代理写可导入的 IMDSv2 守卫包，供元数据请求转发调用：获取令牌须为 PUT /latest/api/token 并带 X-aws-ec2-metadata-token-ttl-seconds（1 到 21600 的整数）；请求里带 X-Forwarded-For、Forwarded 或 Via 头一律拒绝；读取元数据须带有效令牌头，令牌绑定签发时的源容器标识，换容器使用拒绝；令牌 TTL 到期后拒绝；GET 请求不得带令牌 TTL 头；路径含 .. 段或百分号编码的 / 拒绝。用带 X-Forwarded-For、跨容器令牌、TTL 越界、GET 带 TTL 头、令牌过期和路径穿越验证。
