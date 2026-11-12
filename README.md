# AuthorizationPolicy主体与明文

网格里 AuthorizationPolicy 的 notPrincipals 对未走 mTLS 的明文请求会因为没有主体而成立，攻击者因此绕过“除某服务外均放行”的写法。为Go实现可导入的策略评估包：按 CUSTOM、DENY、ALLOW 的顺序评估，工作负载没有任何策略时放行，只有 ALLOW 策略而无规则命中时拒绝，空 ALLOW 规则集拒绝一切；principals 取自客户端证书的 SPIFFE 身份，requestPrincipals 取自 JWT 的 iss/sub，二者不可互换；明文请求没有主体时 notPrincipals 视为不成立并在结果里给出告警。用明文、JWT 与证书混用和多策略叠加验证。
