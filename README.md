# SWIM 周期性心跳探测与间接 Ping 代理探针（TypeScript）

弱网络环境下的分布式故障检测器，零依赖，使用 Node.js 内置测试运行器（`node:test`）验证。

## 协议流程

每个协议周期（`periodMs`）：

1. **直连探测**：节点随机选择一个存活成员发送 `Ping`，等待 `ackTimeoutMs`。
2. **间接探测**：直连 Ack 超时后，随机选择 K 个代理节点发送 `PingReq`；
   代理代为 `Ping` 目标，并将目标的 `Ack` 沿健康的代理链路转发回发起方。
3. **判定**：周期结束前任一路径收到 Ack 则目标保持 `alive`；否则标记 `suspect`，
   怀疑超时（默认 2 个周期）内无 Ack 翻案则标记 `dead`。

间接探针让"直连丢包但节点存活"的场景不再产生假阳性。

## 文件结构

- `src/clock.ts` — 确定性虚拟时钟（测试时钟），驱动协议与网络事件
- `src/network.ts` — 模拟网络：单向时延、定向链路丢包、消息日志
- `src/swim.ts` — SWIM 节点：周期探测、K 路间接代理探针、成员状态机
- `tests/swim.test.ts` — `node:test` 测试（含验收场景）
- `examples/demo.ts` — 可运行的弱网络场景演示

## 运行要求

Node.js ≥ 23.6（原生 TypeScript 类型擦除，无需编译与任何依赖）；
Node 22.6–22.17 需加 `--experimental-strip-types` 运行。

```sh
npm test        # node --test tests/swim.test.ts
npm run demo    # node examples/demo.ts
```

## 运行样例（`npm run demo`）

场景：A↔B 直连链路全部丢包，代理 C/D 链路通畅，K=2。

```
Message timeline:
  [t=1000ms]  X A -> B  PING seq=1
  [t=1300ms]    A -> C  PING-REQ target=B seq=1
  [t=1300ms]    A -> D  PING-REQ target=B seq=1
  [t=1305ms]    C -> B  PING seq=1 (on behalf of A)
  [t=1305ms]    D -> B  PING seq=1 (on behalf of A)
  [t=1310ms]    B -> C  ACK  seq=1 (relayed for A)
  [t=1310ms]    B -> D  ACK  seq=1 (relayed for A)
  [t=1315ms]    C -> A  ACK  seq=1 (target=B)
  [t=1315ms]    D -> A  ACK  seq=1 (target=B)

A's membership view after the run:
  B: alive
  C: alive
  D: alive
```

## 测试结果（`npm test`，Node v23.9.0）

```
✔ acceptance: direct A->B loss triggers K indirect probes via C/D, B stays alive
✔ healthy direct path: Ack without any PingReq
✔ total isolation: target becomes suspect, then dead after suspicion timeout
✔ K is capped by the number of available proxy members
ℹ tests 4  ℹ pass 4  ℹ fail 0
```

验收场景：测试时钟下构造 A↔B 直连丢包、代理通畅；A 直连 B 超时后触发
C/D 代理探针，并在周期时限内（t=1315ms < 2000ms）经代理收到 B 的 Ack，
B 全程保持 `alive`，未被错误标记为 Down。
