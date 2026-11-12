// Package netsec 提供带逐跳授权的 DHCPv6 中继封装。
//
// 可信中继在封装 RELAY-FORW 时把入端口身份（Interface-ID）、事务号和
// 客户身份绑定记录到本地；来自不受信客户链路的报文若携带任何中继专属
// 字段（中继消息类型、Relay-Msg、Interface-ID）一律拒绝。服务器按入向
// 链路的逆序构造 RELAY-REPL，中继剥离最外层前必须确认该层的端口身份、
// 事务、客户身份与跳数都与本跳记录一致，防止客户端预伪造或跨事务串用。
package netsec
