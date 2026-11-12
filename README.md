# TDX报告数据与TD属性

TDX 远程证明里，攻击者可把另一个合法 TD 的 quote 配在自己的 TLS 密钥上，或在调试 TD 里跑相同的度量。为Go证明服务写供 QvE 验签后调用的 TD 报告核对包：REPORTDATA 64 字节须等于 SHA-512(随机数‖TLS 公钥 SPKI‖会话标识)，TDATTRIBUTES 的 DEBUG 位必须为零，MRTD 与四个 RTMR 与参考值逐项比较，TCB 状态只接受 UpToDate 与调用方登记的 SWHardeningNeeded 豁免。用换密钥、调试属性、RTMR 单项偏差、过期 TCB 与截断报告验证。
