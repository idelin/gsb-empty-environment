# QUIC反放大预算

QUIC 服务端在客户端地址验证完成前发出的数据量不得超过收到的 3 倍，攻击者伪造源地址发小包，借大握手响应放大流量。为Go QUIC 服务端写供发送调度调用的反放大预算包（RFC 9000 第 8.1 节）：未验证地址的路径，已发字节数不得超过该路径已收 UDP 载荷字节的 3 倍；成功解密该连接的 Handshake 包或收到有效的 Retry 或 NEW_TOKEN 令牌后视为已验证；预算耗尽时发送被阻塞，不得靠 PTO 探测包突破；客户端 Initial 数据报小于 1200 字节时服务端丢弃且不计入预算；连接迁移到新路径后，该路径单独计预算，PATH_CHALLENGE 先于大数据发送。用 1200 字节 Initial 对应 3600 字节预算、PTO 探测、小 Initial 和迁移新路径验证。

## 使用方式

发送调度在构造数据报之前调用 `Reserve`，成功后再实际发送；若构造失败，用 `Refund` 传入相同的发送类型归还已预留的字节。

```go
budget := netsec.NewBudget()

// 收到客户端 Initial：小于 1200 字节时返回 false，且不计入预算。
if budget.ReceiveInitial(path, udpPayloadLen) {
	// 1200 字节 Initial 产生 3600 字节发送预算。
}

// 普通数据、PTO 探测和 PATH_CHALLENGE 都受同一预算约束。
if budget.Reserve(path, datagramLen, netsec.SendData) {
	// 写入 UDP socket。
}

// 成功解密客户端 Handshake，或验证 Retry / NEW_TOKEN 令牌后解除限制。
budget.OnHandshakeDecrypted(path)
budget.OnValidToken(path)

// 连接迁移：新路径独立记账，并且先发送 PATH_CHALLENGE。
budget.ReceiveDatagram(newPath, migrationDatagramLen)
budget.MigrateTo(newPath)
budget.Reserve(newPath, challengeLen, netsec.SendPathChallenge)
```
