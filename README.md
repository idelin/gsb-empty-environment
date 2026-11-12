# mbuf链越界校验

收包路径上网卡或虚拟设备交来的 mbuf 链，其 data_off、data_len、pkt_len、nb_segs 字段可被不可信的客户机驱动污染，攻击者借此越出 mbuf 缓冲读取相邻数据。为C++17数据面写供解析前调用的 mbuf 链校验库：每段 data_off+data_len 不得超过 buf_len，各段 data_len 之和等于首段 pkt_len，nb_segs 与实际段数一致，链中出现环就拒绝，mbuf 指针须落在 mempool 的地址范围内且按元素大小对齐，引用计数不得为零。用越界偏移、长度和不等、环、伪造指针和合法链验证。
