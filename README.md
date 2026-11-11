# conntrack

用户态防火墙放行了伪造的RST和窗口外数据，攻击者能断开他人的连接。给Go防火墙实现TCP连接跟踪包：按序列号和窗口缩放跟踪双向可接受范围，处理同时打开、半关闭、SYN重传和序列号回绕；RST只在序列号精确匹配时生效，落在窗口内时改发挑战ACK。用生成的报文序列与参考状态模型比对每个报文的放行结果。

## 设计

包 `conntrack` 实现一个有状态的 TCP 中间盒跟踪器，对每个报文返回三种裁决：

- `VerdictAccept`：合法，放行；
- `VerdictDrop`：非法或窗口外，丢弃；
- `VerdictChallengeACK`：报文可疑（窗口内 RST/SYN、非法 ACK），不放行，由防火墙回送挑战 ACK。

### 关键安全策略

- **RST（RFC 5961）**：仅当 `Seq == rcvNxt` 精确匹配下一个期望序号时才拆除连接；
  序号落在接收窗口内但不精确时返回 `VerdictChallengeACK` 且保留连接；窗口外直接丢弃。
- **窗口跟踪**：每个方向独立维护 `rcvNxt`、接收窗口和窗口缩放因子；报文的 Window
  字段只更新**发送方自己**的接收窗口，SYN 中的窗口不缩放，非 SYN 窗口按对端宣告的
  shift 放大。
- **数据可接受性（RFC 793）**：数据段需与接收窗口 `[rcvNxt, rcvNxt+rcvWnd)` 有重叠；
  零窗口时只允许窗口边缘的零长度段；按序前缀推进 `rcvNxt`，乱序但在窗口内仍放行。
- **ACK 校验**：ACK 号必须落在发送方 `(ISN, sndNxt]` 区间内，采用相对 ISN 的模 2^32
  差值判断，序列号回绕时依然正确；非法 ACK 触发挑战 ACK。
- **握手与异常**：支持三次握手、同时打开、SYN/SYN-ACK 重传（ISN 必须一致且不得重新
  协商缩放）；已建立连接上收到 SYN 永不重置，窗口内则挑战 ACK。
- **半关闭**：完整覆盖 `FIN_WAIT_1/2`、`CLOSE_WAIT`、`CLOSING`、`LAST_ACK`、`TIME_WAIT`，
  含同时关闭、FIN 重传和回绕序号。
- **序号回绕**：全部序号运算基于 `uint32` 自然回绕与 `int32` 有符号差值比较。

## 文件

- `conntrack.go`：标志位、裁决、状态、`Packet`/`ConnKey` 类型；
- `sequence.go`：模 2^32 序号比较与窗口可接受性；
- `connection.go`：连接与每端（endpoint）状态；
- `processing.go`：核心状态机；
- `table.go`：并发安全的连接表、查找与超时清理。

## 用法

```go
tbl := conntrack.NewTable()
verdict := tbl.Process(conntrack.Packet{
    SrcIP: srcIP, DstIP: dstIP,
    SrcPort: srcPort, DstPort: dstPort,
    Seq: seq, Ack: ack, Flags: conntrack.FlagACK,
    Window: win, Payload: payloadLen,
})
switch verdict {
case conntrack.VerdictAccept:      // 放行
case conntrack.VerdictDrop:        // 丢弃
case conntrack.VerdictChallengeACK: // 丢弃并回送挑战 ACK
}
```

`Table.ProcessAt(p, now)` 接受显式时间戳，便于确定性测试；`Sweep(now)` 清理握手、
空闲和关闭超时的连接。

## 测试

- `conntrack_test.go`、`edge_test.go`：握手、RST 策略、窗口缩放、零窗口、回绕、
  同时打开/关闭、SYN 重传、半关闭、并发与超时的手写用例；
- `ref_model_test.go`：**独立参考模型**，用 int64 绝对序号空间实现同一策略，
  与生产代码不共享任何序号运算；
- `differential_test.go`、`stress_test.go`：生成随机报文序列（含攻击报文、回绕 ISN、
  零窗口、各类握手/关闭路径），逐包比对生产实现与参考模型的裁决。

运行：

```sh
go test ./...
go test -race ./...
```
