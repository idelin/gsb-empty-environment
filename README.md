# IKEv2半开连接阈值与Cookie通知

攻击者用伪造源地址发海量 IKE_SA_INIT，把响应方的半开连接表和 DH 运算耗尽。为Go VPN 网关写可导入的 IKE_SA_INIT 准入包：半开连接数达到阈值 200 后，对没有 COOKIE 通知的请求只回 COOKIE（值为 HMAC(轮换秘密, Ni、源地址、SPIi)），不做 DH；重发的 IKE_SA_INIT 带回 COOKIE 且匹配才处理，Ni 须与计算 COOKIE 时一致；秘密轮换后上一把仍可用一个周期。用无 Cookie、换 Ni、换源地址和正常流程验证。
