# 双向流量控制信标与滑动窗口协调器

零依赖、纯 Node.js 内置模块实现的轻量级滑动窗口流控协议（TypeScript 编写，
利用 Node.js 原生类型擦除直接运行，无需编译步骤）。

## 协议设计

- **帧头（16 字节定长）**：`Type(1) | Flags(1) | PayloadLength(2) | Seq(4) | CumulativeACK(4) | WindowSize(4)`，
  见 `src/frame.ts` 中的格式图。
- **双向信标**：每个帧（DATA / ACK / WINDOW_PROBE）都捎带发送方自己的
  `CumulativeACK + WindowSize`，因此流控信号天然双向流动；纯 ACK 帧即"流量控制信标"。
- **发送端滑动窗口**（`src/sender.ts`）：严格满足 `BytesInFlight ≤ 最新信标 WindowSize`。
  在途字节填满窗口立即挂起（`suspend` 事件）；收到窗口扩大信标后恢复（`resume` 事件）；
  窗口归零进入探测模式，按 `probeIntervalMs` 周期发送 `WINDOW_PROBE`，直到窗口重新打开。
- **接收端**（`src/receiver.ts`）：通告窗口恒为 `max(0, capacity - buffered)`；
  应用按自身速度 `consume()`，消费即触发窗口扩大信标；`setCapacity()` 主动下发
  窗口缩小/扩大通知。遵循 RFC 793 语义：窗口收缩后，仍接受"历史已通告右沿"
  （`advertisedEdge`）之内的在途数据，避免收缩竞态导致死锁。
- **链路**（`src/link.ts`）：内存点对点链路，可配置单向时延，支持帧观察者（测试/演示用）。

## 运行要求

- Node.js ≥ 23.6（原生运行 TypeScript）；Node 22.6+ 亦可（脚本已带
  `--experimental-strip-types` 标志）。
- 无任何 npm 依赖；`npm run typecheck` 需要自行安装 `typescript` 与 `@types/node`。

```bash
npm test       # Node.js 内置测试运行器（node:test）
npm run demo   # 运行验收场景样例
```

## 验收场景（examples/demo.ts）

发送端快速注入 1 MiB 文件；接收端 64 KiB 缓冲区、每 25 ms 仅消费 4 KiB；
t+400ms 主动下发窗口缩小通知（64→12 KiB），t+1200ms 恢复。实测输出节选：

```
[t+   97ms]   !! SUSPEND  in-flight=0B fills window=0B, queued=1048576B
[t+  121ms]   <- BEACON ack=65536 window=0 (receive buffer full)
[t+  506ms] -- receiver pushes WINDOW-SHRINK notification: 64KiB -> 12KiB
[t+  560ms]   ?? PROBE #1  window is zero, probing peer...
[t+ 1455ms] -- receiver pushes WINDOW-GROW notification: 12KiB -> 64KiB
[t+ 6788ms]   >> RESUME   window=4096B, in-flight=4096B
[t+ 6813ms] transfer complete in 6813ms
[t+ 6813ms] sender stats:   { dataFramesSent: 235, dataBytesSent: 1048576,
                              probesSent: 8, suspensions: 1, beaconsReceived: 486 }
[t+ 6814ms] receiver stats: { dataFramesReceived: 235, dataBytesReceived: 1048576,
                              beaconsSent: 487, outOfWindowDrops: 0, probesAnswered: 8 }
[t+ 6814ms] integrity check (sha256): PASS
```

## 测试结果（node:test，7 项全部通过）

```
✔ frame codec round-trips DATA and beacon frames
✔ receiver beacons window = capacity - buffered, grows back on consume
✔ sender suspends when in-flight bytes fill the window, resumes on growth beacon
✔ fast injection vs slow consumer: sender parks at the window edge, integrity holds
✔ window-shrink notification throttles sender until window grows again
✔ zero window enters probe mode; sender stays parked until window reopens
✔ bidirectional simultaneous transfer with piggybacked beacons
ℹ tests 7  ℹ pass 7  ℹ fail 0
```

其中集成测试通过链路观察者持续断言核心不变量：
**任一 DATA 帧的发送时刻都满足 `seq + len ≤ 最新信标 ack + window`**，
并验证零窗口期间没有任何 DATA 帧发出、窗口重开后探测停止、双向传输数据完整（SHA-256）。

## 目录结构

```
src/frame.ts       帧格式编解码（DATA / ACK / WINDOW_PROBE）
src/sender.ts      滑动窗口发送端（挂起/恢复/零窗口探测）
src/receiver.ts    流控接收端（窗口信标、消费限速、容量调整）
src/endpoint.ts    双向端点（收发一体，信标捎带）
src/link.ts        内存链路（时延、帧观察者）
test/flow-control.test.ts   node:test 测试套件
examples/demo.ts   验收场景演示
```
