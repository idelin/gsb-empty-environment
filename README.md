# SWIM 故障检测器（TypeScript）

弱网络环境下的分布式故障检测：每个协议周期随机选择目标发送 `Ping`；
直连 `Ack` 超时后，随机选择 **K 个间接代理节点**发送 `PingReq` 协助探测，
任一路径在周期内带回 `Ack` 即判定存活，大幅降低弱网下的假阳性率。

零依赖。TypeScript 源码由 Node.js 原生类型擦除直接执行（Node >= 22.6），
测试使用 Node 内置 `node:test` + `node:assert`。

## 结构

- `src/swim.ts` — `SwimNode`：周期探测、PingReq 间接探测、代理转发、成员状态
- `src/messages.ts` — 协议消息：`Ping` / `Ack` / `PingReq`
- `src/transport.ts` — `Transport` 接口与 `SimulatedNetwork`（固定时延 + 可配置单向丢包链路）
- `src/clock.ts` — `Clock` 抽象：`ManualClock`（测试时钟）/ `SystemClock`
- `test/swim.test.ts` — 内置测试（含验收场景）
- `examples/demo.ts` — 真实时钟运行样例

## 协议时序（一个周期内）

```
A --Ping(B)----------X   直连丢包，pingTimeoutMs(300ms) 后超时
A --PingReq(B)--> C,D    触发 K=2 个代理
C --Ping(B)--> B --Ack--> C --Ack(target=B)--> A   代理路径通畅
A 在 indirectTimeoutMs(500ms) 内收到 Ack => B 保持 Alive
```

## 运行

```bash
# 需要 Node >= 22.6（--experimental-strip-types）；Node >= 23.6 可省略该 flag
npm test    # 内置测试
npm run demo  # 运行样例（约 3.6s）
```

## 测试结果

```
✔ 直连通畅时：Ping 直接收到 Ack，不触发 PingReq
✔ 验收场景：A 直连 B 丢包超时，C/D 代理探针在时限内带回 Ack，B 不被误判 Down
✔ B 完全失联（所有路径均丢包）：直连与间接探测都超时，B 被标记 Down
✔ 被标记 Down 的节点不再作为探测目标或代理
ℹ tests 4  ℹ pass 4  ℹ fail 0
```

## 运行样例输出（节选）

```
SWIM demo: A<->B 直连丢包，A 通过代理 C/D 间接探测 B

[+1019ms] {"type":"period-start","node":"A","seq":1,"target":"B"}
[+1019ms] {"type":"ping","node":"A","seq":1,"target":"B"}
[+1333ms] {"type":"direct-timeout","node":"A","seq":1,"target":"B"}
[+1333ms] {"type":"ping-req","node":"A","seq":1,"target":"B","proxies":["C","D"]}
[+1474ms] {"type":"ack","node":"A","seq":1,"target":"B","via":"C"}
[+1475ms] {"type":"probe-success","node":"A","seq":1,"target":"B"}
...
最终成员视图：
  节点 A: B=alive C=alive D=alive
  节点 B: A=alive C=alive D=alive
  节点 C: A=alive B=alive D=alive
  节点 D: A=alive B=alive C=alive
```

## 验收场景说明（test/swim.test.ts）

`ManualClock` 虚拟时间下构造 4 节点集群，`SimulatedNetwork` 配置
A↔B 直连双向丢包、A↔C/A↔D/C↔B/D↔B 通畅。推进时钟：t=1000ms A 发出
直连 Ping（被丢弃）→ t=1300ms 直连超时，A 向 C、D 发送 PingReq →
代理路径往返约 40ms 后 A 收到 `Ack(target=B, via=C)`，远在 500ms 间接
超时之前。断言：B 在 A 的视图中保持 `alive`，无 `mark-down` 事件。
