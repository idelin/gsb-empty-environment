# OSCORE 重放窗口与重启恢复

`netsec.Guard` 是供 CoAP 处理器调用的 OSCORE 请求重放守卫，每个安全上下文一个实例，可被多个处理 goroutine 并发使用。

## 行为

- PIV 按大端 1–5 字节解析，最大值为 `2^40-1`。
- 已确认窗口后使用宽度为 32 的滑动窗口；窗口内已见或低于窗口下沿的 PIV 返回 `ErrReplay` / `ErrBelowWindow`。
- 新建（或调用 `ForgetWindow()`）后窗口未知，所有请求先返回 `StatusEchoRequired`。处理器应回复受 OSCORE 保护的 CoAP `4.01 Unauthorized`（响应使用服务端自己的 PIV），并把 `Decision.Echo` 放入 RFC 9175 Echo 选项（选项号 `252`）。
- 客户端在受 OSCORE 保护的后续请求中回显该值后，该请求的 PIV 成为新窗口下沿；低于它的旧 PIV 仍会被拒绝，避免恢复窗口后接受重启前抓到的包。
- 接受最后一个可用 PIV 后上下文永久停用，之后所有请求返回 `ErrSequenceExhausted`。

## 处理器用法

```go
guard := netsec.NewGuard() // 每个 OSCORE 安全上下文一个

// 必须在 OSCORE 解密、认证成功之后调用。
decision := guard.Check(piv, echo) // 请求中受保护的 PIV 与 Echo 选项
switch decision.Status {
case netsec.StatusEchoRequired:
	// 回复 4.01 Unauthorized，并添加 Echo 选项 252，值为 decision.Echo。
case netsec.StatusRejected:
	// 丢弃请求；decision.Err 给出具体原因。
case netsec.StatusAccepted:
	// 正常处理 CoAP 请求。
}
```

Echo 值由 `crypto/rand` 生成，默认有效期 60 秒；回显成功即消费，失败不会推进窗口。不要从未认证的外层选项中取值。

## 验证

`go test -race ./...` 覆盖重放、32 宽窗口上下边界、重启后无 Echo/陈旧 Echo/恢复下沿重放，以及 5 字节 PIV 耗尽后的停用行为。
