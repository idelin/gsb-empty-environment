# SVCB参数顺序与mandatory

攻击者篡改 HTTPS 记录的 SvcParams，想让客户端丢掉 ech 或套用冲突参数。为Go解析宿主写可导入的 SVCB 线格式校验包，以下按本题约定：键严格升序且不重复；mandatory 列表升序、不含自身，所列键都须存在，客户端不支持其中任一键就忽略记录；no-default-alpn 值为空且须配 alpn；ipv4hint 长度为4的倍数，ipv6hint 为16的倍数；SvcPriority=0 时不得带参数。用线格式字节测各违规。
