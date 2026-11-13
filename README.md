# QUIC连接ID帧上限与退役放大

QUIC 对端可以连续发送 NEW_CONNECTION_ID 与 RETIRE_CONNECTION_ID，攻击者借此让本端保存海量连接 ID，或让每次退役都触发本端补发新 ID 以放大流量。为C++17 QUIC 栈写供帧处理循环调用的连接 ID 管理库，active_connection_id_limit 由本端传输参数给出：对端发来 NEW_CONNECTION_ID 时，CID 长度须在 1 到 20，否则 FRAME_ENCODING_ERROR；retire_prior_to 大于序号为 FRAME_ENCODING_ERROR；序号相同而内容不同为 PROTOCOL_VIOLATION，内容相同的重复帧无操作；处理退役要求后仍保存的有效 ID 数超过限制为 CONNECTION_ID_LIMIT_ERROR；零长度 CID 的连接收到该帧为 PROTOCOL_VIOLATION；对端发来 RETIRE_CONNECTION_ID，序号大于等于本端已发放的最大序号加一为 PROTOCOL_VIOLATION，重复退役同一序号无操作；本端因退役补发新 ID 的累计数量不得超过已退役数，且每个到达的数据包最多触发一次补发。用超限、序号相同内容不同、retire_prior_to 越界、洪泛退役和零长度连接验证。
