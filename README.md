# 早期数据字节上限

0-RTT数据超过票据里的max_early_data仍被解密交付。累计早期数据超过该上限则失败，已交付的超额字节不能再交给应用。上限之内的早期数据可以交付。可导入的Go状态机接TLS栈送来的状态。发送上限加一个字节。

## 修复模型

`netsec` 包提供一个可导入、可并发使用的状态机 `Machine`。TLS 栈把连接事件
（`Event`）送入 `Machine.Handle`，再按返回的 `Action` 决定哪些字节能交给应用：

- 累计 0-RTT 字节数仍在票据 `max_early_data` 之内：返回 `ActionDeliver`，可交付。
- 某条记录跨越上限：在边界处切分，上限内前缀 `ActionDeliver`，超出部分计入
  `ActionFail.WithheldBytes` 并扣留，状态进入 `StateFailed`。
- 进入 `StateFailed` 后，后续每条早期数据记录都只返回 `ActionWithhold`，
  已解密的超额字节永远不会再交给应用（fail-closed）。
- 只有 `ActionDeliver` 的 `Payload` 可交付；`Withhold`/`Fail`/`Drop` 都不可交付。

## 事件与状态

- 事件：`EventStart`（携带票据的 `MaxEarlyData`）、`EventBeginEarlyData`、
  `EventRejectEarlyData`、`EventEndOfEarlyData`、`EventHandshakeComplete`、
  `EventEarlyData`、`EventApplicationData`、`EventAlert`、`EventClose`。
- 状态：`Idle → Handshake → EarlyData → Handshake → Connected`，异常进入
  `Failed`，正常收尾为 `Closed`。

## 用法

```go
m := netsec.New()
m.Handle(netsec.EventStart{MaxEarlyData: 5}) // 票据里的 max_early_data
m.Handle(netsec.EventBeginEarlyData{})

// 攻击/越界：发送“上限 + 1”个字节。
actions, err := m.Handle(netsec.EventEarlyData{Payload: []byte("ABCDEF")})
for _, a := range actions {
    if a.Kind == netsec.ActionDeliver {
        // 仅前 5 字节到达应用；第 6 字节被 Withhold。
        applicationWrite(a.Payload)
    }
}
// err 命中 ErrEarlyDataLimitExceeded，m.State() == StateFailed。
```

## 验证

- `go test -race ./...`：含“上限 + 1 字节”越界、精确等于上限不失败、零上限、
  服务端拒绝 0-RTT、失败后继续扣留、并发记账等用例，并断言“已交付字节数永不超过
  `max_early_data`”这一不变量。
- `ExampleMachine` 演示越界一个字节时仅交付上限内前缀。
