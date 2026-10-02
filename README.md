# swim — SWIM 周期性心跳探测与间接 Ping 代理探针（Go 版）

弱网络环境下的分布式故障检测器，仅使用 Go 标准库。每个节点按周期随机选择
一个目标发送 `Ping`；若直连 `Ack` 超时，则触发 K 个间接代理节点发送
`PingReq` 协助探测，任一代理在时限内转发回 `Ack` 即判定目标存活，从而大幅
降低弱网下的假阳性率。

## 探测协议

```
正常路径:   A --Ping(seq)------------> B
            A <-----------Ack(seq)---- B

直连丢包:   A --Ping(seq)----X (丢失)  B
            A 等待 AckTimeout 后升级为间接 间接探测:
            A --PingReq(target=B)--> C, D   (K=2 个代理)
            C --Ping(seq, fwd=A)---> B
            C <---Ack(seq, fwd=A)--  B
            C --Ack(seq, via=C)----> A      (代理转发回发起者)
```

状态机：探测失败（直连 + 间接均超时）时 `Alive -> Suspect`，再次失败
`Suspect -> Down`；任何来自该节点的有效消息都会将其恢复为 `Alive`。

## 代码结构

- `clock.go` — `Clock`/`Timer` 抽象：`RealClock`（真实时钟）与
  `ManualClock`（测试时钟，`Advance` 确定性推进）
- `message.go` — `Ping` / `Ack` / `PingReq` 消息格式
- `transport.go` — `Transport` 接口与内存 `Network`，支持按有向链路丢包，
  用于模拟弱网
- `node.go` — 节点探测主循环：周期随机选目标 → 直连 Ping → K 个代理
  PingReq → 状态迁移；并暴露 `Event` 观测事件
- `node_test.go` — 验收测试（测试时钟驱动）
- `cmd/swimdemo/main.go` — 真实时钟下的 4 节点运行样例

## 运行测试

```sh
go test ./... -race -v
```

测试结果（Go 1.11.4 darwin/amd64）：

```
=== RUN   TestIndirectProbePreventsFalsePositive
--- PASS: TestIndirectProbePreventsFalsePositive (0.56s)
=== RUN   TestUnreachableNodeMarkedDown
--- PASS: TestUnreachableNodeMarkedDown (0.91s)
=== RUN   TestDirectPingSuccess
--- PASS: TestDirectPingSuccess (0.23s)
PASS
ok  	swim	5.661s
```

### 验收场景说明

`TestIndirectProbePreventsFalsePositive` 在测试时钟下构造「直连丢包、代理
通畅」：链路 `A<->B` 双向丢包，其余链路正常，`K=2`。

1. 时钟推进 1 个周期，A 向 B 发出直连 `Ping`，被丢弃；
2. 推进 `AckTimeout`，A 触发间接探测，事件确认代理恰为 `[C D]`；
3. C/D 向 B 发 `Ping` 并在间接时限内将 B 的 `Ack` 转发回 A
   （事件 `ack from B via C|D`）；
4. 断言 B 在 A 处始终保持 `Alive`，无任何状态变更事件 —— 未被错误标记为
   `Down`。

`TestUnreachableNodeMarkedDown` 验证反向边界：B 被完全隔离时，A 经两个
失败周期将 B 标记为 `Alive -> Suspect -> Down`。

## 运行样例

```sh
go run ./cmd/swimdemo
```

4 节点集群（真实时钟，周期 500ms，AckTimeout 150ms，IndirectTimeout
300ms，K=2）。启动后切断 `A<->B` 直连链路；约 3 秒后停掉 D 模拟宕机。
输出节选：

```
== weak network: direct link A<->B drops all packets ==
  t= 1151ms  [A] probe start -> B
  t= 1375ms  [A] direct ping to B timed out
  t= 1375ms  [A] indirect probe of B via [C D]
  t= 1375ms  [A] ack from B via D
  t= 1320ms  [B] direct ping to A timed out
  t= 1320ms  [B] indirect probe of A via [D C]
  t= 1324ms  [B] ack from A via C
== node D stopped (simulating a crash) ==
  t= 3714ms  [A] direct ping to D timed out
  t= 3715ms  [A] indirect probe of D via [C B]
  t= 4101ms  [A] probe of D failed (direct + indirect)
  t= 4101ms  [A] D: Alive -> Suspect
  ...
== final membership views ==
  A: B=Alive C=Alive D=Down
  B: A=Alive C=Alive D=Suspect
  C: A=Alive B=Alive D=Down
  D: A=Alive B=Alive C=Alive
```

可见 A/B 之间直连虽断，双方通过代理互探成功、始终互为 `Alive`；真正宕机
的 D 被其余节点按 `Alive -> Suspect -> Down` 逐级判定（B 在采样时刻尚处
`Suspect` 过渡态，下一次失败即降为 `Down`）。
