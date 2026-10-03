# SWIM 周期性心跳探测与间接 Ping 代理探针（Go 版）

弱网络环境下的分布式故障检测器，仅使用 Go 标准库。

## 协议要点

- 每个协议周期随机选取一个对等节点发送 `PING`。
- 直连 `PING` 在 `PingTimeout` 内未收到 `ACK` 时，选取 `K` 个代理节点发送 `PING_REQ`，
  由代理代为直连目标并在 `IndirectTimeout` 内转发 `ACK`，大幅降低弱网络下的假阳性率。
- 状态机：`ALIVE -> SUSPECT -> DOWN`（连续两个周期探测失败才标记 `DOWN`）。
- 时间由 `Clock` 抽象：测试用 `ManualClock`（测试时钟）确定性推进，演示用虚拟时间统计。

## 目录结构

- `swim/` — 协议实现（消息、时钟、模拟传输、节点状态机）
- `swim/node_test.go` — 验收测试（测试时钟下构造直连丢包、代理通畅场景）
- `cmd/swim-demo/` — 运行样例

## 运行测试

```sh
go test -v ./swim/
```

验收场景：A 直连 B 双向丢包，C/D/E 代理链路通畅。A 直连 Ping B 超时后触发
K=2 个代理 PingReq，在时限内收到 Ack，断言 B 保持 `ALIVE` 未被误标为 `Down`。
对照测试：B 完全隔离时按 `SUSPECT -> DOWN` 正确标记。

## 运行样例

```sh
go run ./cmd/swim-demo
```
