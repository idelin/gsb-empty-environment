# HTTP数据报流号解复用隔离

HTTP/3 的 HTTP Datagram 靠 Quarter Stream ID 找到所属请求流，攻击者可以发送指向别人的流、已关闭的流或未协商能力的数据报。为Go的 HTTP/3 服务端写可导入的数据报分发包（RFC 9297，以下处置为本题规定）：双方未都设置 SETTINGS_H3_DATAGRAM 时收到数据报按 H3_DATAGRAM_ERROR 关闭连接；Quarter Stream ID 乘 4 后须是该连接上对端发起的双向流，且该流已用 CONNECT 类请求建立数据报通道，否则静默丢弃；该流尚未创建时最多缓冲 16 个数据报，满后丢弃最旧的；Quarter Stream ID 超过 2^60 - 1 按 H3_DATAGRAM_ERROR 处理。测未协商、不属于本连接的流号、已关闭流、缓冲溢出。
