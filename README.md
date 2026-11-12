# IKEv2 EAP-only认证降级拒绝

IKEv2 的 EAP-only 认证让服务器不出示证书，攻击者在路径上可伪装成服务器，借强度不足的 EAP 方法拿走凭据。给Go IKEv2 发起方写供 IKE_AUTH 应答处理调用的认证策略包，按 RFC 5998：仅当请求里带了 EAP_ONLY_AUTHENTICATION 通知（类型 16417）且应答也带该通知时才接受不含 AUTH 载荷的服务器认证；此时 EAP 方法必须在调用方允许列表内（至少含 EAP-TLS 13，EAP-AKA' 50，不得含 MSCHAPv2 26 与 GTC 6）；服务器未回该通知却缺 AUTH 载荷，或回了通知但方法不在列表内，都以认证失败结束，不得退回只验用户名口令。用服务器缺 AUTH、通知不对称、MSCHAPv2 和合法 EAP-TLS 验证。
