# XMPP stanza来源与目标路由校验

XMPP 服务器转发 stanza 时，攻击者可在客户端流里伪造他人的 from，或让另一台服务器以未经认证的域名发 stanza。给Go XMPP 路由器写供 stanza 入口调用的校验包，按 RFC 6120 第 8 节：客户端流里 from 缺省时填入已绑定 JID，带 from 但与绑定的完整或裸 JID 不一致时以 invalid-from 流错误关闭；服务器间流里 from 的域名必须是该流上已认证（或已验证）的域名，否则同样以 invalid-from 关闭；服务器间流里 to 的域名不是本机托管域名时丢弃，不转发；绑定前收到的 stanza 一律丢弃。用冒用他人 JID、同一用户另一资源、未认证域名的 s2s、绑定前 stanza 验证。
