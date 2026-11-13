# 客户端断言受众注入与重放

授权服务器用 private_key_jwt 认证客户端时，如果断言的 aud 允许写成多个端点中的任意一个，攻击者就能把客户端给某个端点签的断言拿到另一个端点重放。为Go授权服务器写可导入的客户端断言校验包，签名由回调验证：alg 取调用方白名单（拒绝 none 与 HS 系列）；iss 与 sub 都必须等于 client_id；aud 必须是单个字符串，且逐字等于本服务器的 issuer 标识，数组形式一律拒绝，即使数组里有正确值；exp 必填，且 exp 减 iat 不超过 300 秒，iat 不得比当前时间晚 30 秒以上；jti 必填，在 exp 之前只接受一次，按 client_id 分区；同一请求同时带 client_secret 与 client_assertion 时拒绝；client_assertion_type 须为 urn:ietf:params:oauth:client-assertion-type:jwt-bearer。用 aud 数组含正确值、aud 为令牌端点地址、jti 重放、有效期过长和双重凭据验证。
