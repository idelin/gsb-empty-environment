# WS-Security Timestamp校验

SOAP 服务靠 WS-Security 的 Timestamp 与 UsernameToken 限制重放，攻击者拿旧报文重放，或把已签名的 Timestamp 挪走、另放一个新的。为Go的 SOAP 网关写可导入的 WS-Security 头校验包（WS-Security 1.1）：Security 头里恰有一个 Timestamp，Created 不得晚于当前时间超过 60 秒，Expires 须晚于 Created 且二者间隔不超过 300 秒，已过期拒绝；Timestamp 与 Body 的 wsu:Id 须各自恰出现一次，并被调用方注入的 XML 签名校验函数列入已签名引用集合，未被签名或 Id 重复拒绝；UsernameToken 的 Nonce 与 Created 组合在窗口内只能使用一次。测重放、双 Timestamp、未签名 Timestamp、Id 重复。
