# WireGuard过载下的Cookie应答与MAC2

握手洪泛时，攻击者伪造源地址的初始化消息，想让服务端大量计算或把应答反射给第三方。为Go WireGuard 实现写可导入的握手准入包：所有握手消息先验 mac1；负载超阈值时无有效 mac2 的消息只回 cookie 应答、不做 Curve25519；cookie 为 MAC(每两分钟轮换的秘密, 源 IP 加端口)，应答不长于触发它的消息；mac2 基于上一个 cookie 且 2 分钟内有效。用无 mac1、过载无 mac2、过期 cookie 验证。
