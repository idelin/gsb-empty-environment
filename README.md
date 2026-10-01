# tcprecon — 离散事件 TCP 字节流重建器

在**离散事件模型**中按连接四元组和方向重建字节流。所有包、队列和链路都
以整数 tick 推进，**不发任何真实网络流量**。仅使用 Go 标准库。

## 模型与保证

- **四元组 + 方向**：连接由 `(clientIP, clientPort, serverIP, serverPort)`
  键控；`c->s` 与 `s->c` 两个方向使用**相互独立的序号空间**。
- **重传与重叠冲突**：完全相同的重传段被静默吸收；重叠但字节不同的段被
  记录为**冲突**（保留先到的字节，逐字节列出冲突位置）。
- **缺口不填造**：缺失的序号区间输出为 `[GAP n]` 标记，绝不编造数据。
- **半关闭**：单方向 FIN 只关闭该方向，另一方向可继续收发；双向 FIN 或
  RST 后连接进入 TIME_WAIT。
- **连接 epoch**：每个包携带发送方连接化身号（epoch）。TIME_WAIT 期间
  拒绝新 SYN 与迟到包；旧 epoch 的迟到包被明确丢弃（`stale epoch`），
  **同四元组的旧连接包永远不会混入新连接**。

## 运行

```sh
go run . -script examples/demo.txt     # 或不带参数从 stdin 读脚本
go test ./...                          # 运行全部测试
```

## 脚本格式

```
CONN <id> <client-ip:port> <server-ip:port>
SET delay=<n> timewait=<n>
<tick> SYN  <id> <c->s|s->c> isn=<n> [delay=<n>]
<tick> DATA <id> <c->s|s->c> seq=<n> "payload" [delay=<n>]
<tick> FIN  <id> <c->s|s->c> [seq=<n>] [delay=<n>]
<tick> RST  <id> <c->s|s->c> [delay=<n>]
```

`delay` 是链路延迟（tick），不同延迟可造成乱序与迟到包；`#` 为注释。

## 运行样例（`examples/demo.txt` 的实际输出）

```
=== reconstruction report ===

connection c1 10.0.0.1:4001 <-> 10.0.0.2:80 epoch 1
  final state: closed (both directions FIN)
  c->s: "hello"[GAP 2]"end" [FIN]
  conflicts on c->s: 2 (kept first-seen bytes)
    seq=104 kept 0x6c rejected 0x4c
    seq=105 kept 0x6f rejected 0x4f
  s->c: "200 OK" [FIN]

connection c2 10.0.0.1:4002 <-> 10.0.0.3:443 epoch 1
  final state: closed (RST)
  c->s: "reset me" [RST]
  s->c: (empty) [RST]

connection c1 10.0.0.1:4001 <-> 10.0.0.2:80 epoch 3
  final state: closed (both directions FIN)
  c->s: "ping" [FIN]
  s->c: (empty) [FIN]

dropped packets:
  tick 2: c3 c->s DATA seq=1 len=7 epoch=1 reason=no active connection
  tick 13: c1 c->s SYN seq=700 len=0 epoch=2 reason=SYN during TIME_WAIT
  tick 23: c1 c->s DATA seq=101 len=5 epoch=1 reason=stale epoch (packet=1 conn=3)
```

样例覆盖：重传吸收、重叠冲突（保留先到字节）、`[GAP 2]` 缺口、半关闭后
对向继续发送、TIME_WAIT 拒绝 SYN、epoch 1 的迟到包被 epoch 3 连接拒绝、
RST 中止、无连接数据丢弃。

## 测试结果

`go test -v ./...`：21 个测试全部通过。

```
--- PASS: TestHalfCloseKeepsOtherDirectionOpen
--- PASS: TestStaleEpochCannotMixIntoNewConn
--- PASS: TestSynDuringTimeWaitRejected
--- PASS: TestDataWithoutConnectionDropped
--- PASS: TestRstClosesBothDirections
--- PASS: TestDirectionsHaveIndependentSeqSpaces
--- PASS: TestParseBasic
--- PASS: TestParseErrors
--- PASS: TestEndToEnd
--- PASS: TestLinkDelayReordersDelivery
--- PASS: TestDefaultFinSeqTracksSentBytes
--- PASS: TestInsertInOrderMerges
--- PASS: TestInsertOutOfOrderMerges
--- PASS: TestRetransmissionIdenticalNoConflict
--- PASS: TestOverlapConflictKeepsFirstSeen
--- PASS: TestPartialOverlapConflictAndExtension
--- PASS: TestGapRenderedNotFilled
--- PASS: TestGapUpToFin
--- PASS: TestDataBeyondFinNotRendered
--- PASS: TestInsertSpanningMultipleIntervals
--- PASS: TestEmptyStream
PASS
ok      tcprecon
```

## 代码结构

- `types.go` — 方向、四元组、包、命令定义
- `stream.go` — 单方向流重组：区间插入、冲突检测、`[GAP n]` 渲染
- `observer.go` — 被动观察者：连接表、epoch 隔离、半关闭、TIME_WAIT
- `sim.go` — 离散事件调度器（最小堆）、链路延迟、发送方 epoch/序号跟踪
- `script.go` — 事件脚本解析器
- `report.go` — 重建报告渲染
- `main.go` — CLI 入口
