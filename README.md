# 凭证发行nonce与证明JWT核对

OpenID4VCI 发行方的凭证端点收到持有者的 proof JWT，攻击者拿别家发行方或旧会话的证明来重放。为TypeScript发行服务写可导入的证明校验包：typ 须为 openid4vci-proof+jwt，aud 等于本发行方标识，nonce 须由本服务签发、未过期且只用一次，iat 不早于 nonce 签发且不晚于当前 60 秒，jwk 头含私钥成员 d 则拒绝。
