# IEC 104选择后执行的配对与超时

攻击者在选择命令之后从另一连接或另一信息对象地址发送执行命令，或不经选择直接执行。为Go 104 从站写可导入的控制命令状态包：单点命令（类型标识 45）的 S/E 位为 1 表示选择，须在 30 秒内由同一连接、同一 IOA、同一命令值的执行命令（S/E=0）确认；配置为选择后执行的点收到直接执行则拒绝；取消（COT 8）清除选择；超时的选择作废。用跨连接、错 IOA、超时和直接执行验证。

## 用法

```go
state := netsec.New(
	netsec.WithSelectBeforeOperate(100),
)

selectResult := state.HandleSCO("tcp-conn-1", 6, 100, 0x81) // SELECT, value=true
executeResult := state.HandleSCO("tcp-conn-1", 6, 100, 0x01) // EXECUTE, value=true
```

`HandleCommand`/`HandleCommandAt` 接受已解码的 `ControlCommand`。也可使用
`Select`、`Execute`、`Cancel` 便捷方法。`WithClock` 可注入时钟以便确定性地测试
30 秒超时；连接关闭时应调用 `CloseConnection` 丢弃该连接的全部待处理选择。

安全语义：

- 选择绑定到连接、IOA、命令值和 30 秒截止时间。
- 执行必须由同一连接、同一 IOA、同一命令值确认，且只能消费一次。
- 跨连接、错 IOA、错命令值以及过期选择均不能执行。
- 取消只在同一连接、同一 IOA 上清除选择。
- `PointSelectBeforeOperate` 点无有效选择时拒绝直接执行。
