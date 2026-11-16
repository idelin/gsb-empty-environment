# IEC 104选择后执行的配对与超时

攻击者在选择命令之后从另一连接或另一信息对象地址发送执行命令，或不经选择直接执行。为Go 104 从站写可导入的控制命令状态包：单点命令（类型标识 45）的 S/E 位为 1 表示选择，须在 30 秒内由同一连接、同一 IOA、同一命令值的执行命令（S/E=0）确认；配置为选择后执行的点收到直接执行则拒绝；取消（COT 8）清除选择；超时的选择作废。用跨连接、错 IOA、超时和直接执行验证。

## 设计

包名 `netsec`，仅依赖标准库，`go test ./...` 即可验证。

- `protocol.go`：类型标识 45、S/E 位、COT 4（激活）/8（取消）、30 秒超时常量。
- `command.go`：`SingleCommand`（C_SC_NA_1），三字节小端 IOA + 命令字节的编解码，按 SCS（ON/OFF）比较命令值。
- `state.go`：并发安全的 `StateMachine`，按连接 `ConnID` + IOA 维护待确认选择；线程安全，时钟可注入以便测试超时。

## 安全规则

| 场景 | 结果 |
| --- | --- |
| 选择（S/E=1）后，同连接/同 IOA/同值执行（S/E=0），30 秒内 | `Executed`，选择被消费 |
| 执行来自另一连接 | 拒绝 `ErrConnectionMismatch`，原选择保留 |
| 执行的 IOA 与选择不同 | 拒绝 `ErrSelectRequired` |
| 超过 30 秒才执行 | 拒绝 `ErrSelectionExpired`，选择作废 |
| 选择后执行点收到未经选择的直接执行 | 拒绝 `ErrSelectRequired` |
| 取消（COT 8，同连接） | `Cancelled`，清除选择 |
| 执行命令值与选择不一致 | 拒绝 `ErrValueMismatch` |

## 用法

```go
sm := netsec.NewStateMachine(netsec.RejectUnknownPoints())
sm.AddPoint(100, true) // 100 为选择后执行点

sel := netsec.SingleCommand{IOA: 100, Select: true, On: true}
sm.HandleSingleCommand("conn-1", netsec.COTActivation, sel) // Selected

exec := netsec.SingleCommand{IOA: 100, Select: false, On: true}
d := sm.HandleSingleCommand("conn-2", netsec.COTActivation, exec)
d.Rejected() // true，ErrConnectionMismatch
```

也可使用 `HandleFrame(Frame{Type: TypeSingleCommand, Cause: ..., Conn: ..., Object: wireBytes})`
直接喂入线路上的信息对象字节。测试用 `WithClock` 注入时钟、`WithSelectTimeout`
调整窗口，`Sweep` 与 `ActiveSelection` 用于定时清理和状态巡检。
