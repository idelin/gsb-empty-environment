# APDU命令链完整性守卫

攻击者在智能卡APDU命令链中途插入别的指令，借用卡片已通过的认证状态。给Go重定向代理写供转发循环调用的APDU链守卫：CLA的0x10位表示链接，链内CLA（除该位）、INS、P1、P2须一致且总长受限；被异类指令打断时丢弃整条链并拒绝后续指令直到重建会话；GET RESPONSE(C0)只在上条SW1=61后放行，Le不得超过SW2。用中途插入、跨链拼接和伪造GET RESPONSE验证。

## 实现

`guard.go` 提供 `APDUGuard`。转发循环在发送前调用 `CheckCommand`，收到卡片响应后调用 `ObserveResponse`：

```go
guard := netsec.NewAPDUGuard(4096)

for {
	cmd := readFromClient()
	if err := guard.CheckCommand(cmd); err != nil {
		// 违规命令不转发。若为链违规，守卫会保持 blocked，
		// 后续命令继续以 ErrSessionBlocked 拒绝。
		continue
	}

	rsp := transmitToCard(cmd)
	if err := guard.ObserveResponse(rsp); err != nil {
		continue
	}
	writeToClient(rsp)
}
```

会话重建完成后调用 `guard.ResetSession()`。守卫不会自动复位，避免攻击者用新命令清除违规状态。

约束：

- 带 `CLA & 0x10` 的命令开始或延续链；链内的 `CLA &^ 0x10`、`INS`、`P1`、`P2` 必须一致。
- 链总长度按所有已转发命令的序列化字节数累计，超过构造参数即丢弃整条链并锁死会话。
- 链中插入异类命令、错误结束链、跨链头尾拼接都会触发 `ErrChainHeaderMismatch`，并使 `Blocked()` 为真。
- `INS=0xC0` 仅在上一响应为 `61xx` 时放行，且 `Le`（短格式 0 表示 256）不得超过 `SW2`（`SW2=0` 表示 256）。
- 每个 `61xx` 只授权一次 `GET RESPONSE`；重放、伪造 P1/P2、响应过短或 `Le` 超限都会被拒绝。

运行验证：`go test -race ./...`
