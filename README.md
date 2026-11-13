# 管理帧保护下的解除认证与SA Query

攻击者发未保护的 Deauth，或伪造已关联站点的关联请求，把站点踢下线。为Go接入点管理帧处理器写可导入的 PMF 判定包：已协商 PMF 的站点收到未保护的单播 Deauth 或 Disassoc 一律丢弃；受保护帧的 PN 不大于上一帧丢弃；广播管理帧须有 BIP 完整性且 IPN 递增；已关联站点又发关联请求时回稍后重试并发 SA Query，超时内收到受保护响应则保持关联。用未保护、重放 PN、广播伪造和 SA Query 验证。

## 导入

```go
import netsec "example.org/netsec/q0770"
```

`netsec.Station` 保存每个站点的 `LastPN`、`LastIPN` 和未完成的 SA Query；
`Station.Evaluate`（或 `Handle`）返回 AP 管理帧处理器应执行的动作。

```go
sta := netsec.NewStation(true, true) // 已协商 PMF、当前已关联
d := sta.Handle(netsec.Frame{
	Type:        netsec.FrameDeauth,
	Destination: netsec.DestinationUnicast,
	Protected:   false,
}, time.Now())
if d.Dropped() {
	// 不改变关联状态
}
```

## 判定规则

- `PMF == true` 的已关联站点收到未保护的单播 Deauth/Disassoc：丢弃，关联不变。
- 已验证保护的管理帧：`PN` 必须大于 `LastPN`，否则丢弃；合法帧推进防重放窗口。
- 广播/组播管理帧：仅当 `BIPValid` 为真且 `IPN` 大于 `LastIPN` 时接受。
- 已关联的 PMF 站点再次发送 Association/Reassociation Request：返回状态码 30，并启动 SA Query。
- SA Query Response 必须受保护且事务号匹配；在截止时间前收到时保持关联，超时则解除关联。

`Frame.Protected` 和 `Frame.BIPValid` 必须是解析器**已经完成密码学校验**后的结果，
不能直接来自帧中可伪造的保护标志位。本包负责状态与策略判定，不代替 CCMP/BIP 校验。

## 验证

```sh
go test ./...
go test -race ./...
```
