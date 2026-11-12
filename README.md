# ECH拒绝后的证书身份

启用 ECH 的客户端把真实域名放进内层、外层只露 public_name，攻击者可冒充前端服务器，让握手被拒后返回自己的 retry_configs，或拿 public_name 的证书冒充内层主机。为TypeScript客户端写供握手结果回调使用的 ECH 身份判定组件：ECH 被接受时证书必须对内层 SNI 有效；被拒绝时只能按 public_name 验证证书，通过后才可采用 retry_configs，且不得在该连接发送任何应用数据；重试至多一次，不得回落到不带 ECH。用接受、拒绝配合 public_name 证书、拒绝配合内层名证书、二次拒绝和回落验证。
