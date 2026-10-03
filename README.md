# Network Partition Merging Coordinator (TypeScript)

裂脑恢复合并引擎：模拟 10 节点集群被网络分区为 `[1-5]` 与 `[6-10]` 两个孤立子网，
各自独立演化形成闭环；当跨子网心跳首次打通（首次 PingReq 成功）时，触发合并握手协议，
安全执行拓扑融合与 Lamport 时钟校正，双方平滑合并且无死锁。

## 设计

- **SWIM 风格探测**：周期 `ping`，超时升级为经中介的 `ping-req`，再超时标记
  `suspect -> dead -> removed`。分区期间两侧各自将对侧成员移除，形成闭环。
- **重连探测**：节点对已被移除的引导成员低频发起间接探测（PingReq）。分区愈合后，
  首次跨子网 PingReq 成功，发现对方摘要（digest）不属于本集群，触发合并握手。
- **合并握手**（`merge-propose` / `merge-accept` / `merge-commit`）：
  - 双方同时发起时按 `(epoch, nodeId)` 字典序确定性裁决，小者让渡（yield），杜绝双活僵持；
  - 发起方对两份成员视图做并集合并（SWIM 优先级：incarnation 高者优先，同 incarnation
    悲观状态优先；被标记非 alive 的节点以更高 incarnation 自证存活），`epoch = max+1` 后
    广播 commit，commit 幂等且按 epoch 去重 gossip 至全群；
  - 所有等待均有超时与重试上限，不存在循环等待，保证无死锁。
- **时钟校正**：Lamport 时钟，收消息时 `clock = max(local, remote) + 1`；
  合并时发起方时钟被推高至不低于响应方，commit gossip 使全群时钟快速收敛。

## 结构

- `src/types.ts` — 消息与成员视图类型
- `src/network.ts` — 可分区、可愈合的模拟网络（逐 tick 投递）
- `src/node.ts` — 节点协议：探测、故障检测、合并握手状态机、时钟校正
- `src/simulation.ts` — 仿真驱动（分区/愈合/收敛判定/统计）
- `src/demo.ts` — 运行样例
- `test/merge.test.ts` — Node.js 内置测试（`node:test`）

## 运行

需要 Node.js >= 18（`node:test`）与任意 TypeScript 编译器：

```bash
npm install        # 安装 devDependency: typescript（或自备 tsc）
npm test           # 编译并运行 node:test 测试
npm run demo       # 编译并运行分区-愈合合并样例
```

## 运行样例（节选）

```
=== Phase 2: partition [1-5] | [6-10], each side evolves independently ===
[t=  40] node-2: member 9 -> suspect
[t=  80] node-2: member 9 -> dead
[t= 160] node-1: removed dead member 9; view=[1,2,3,4,5,6,7,8,10]
...
t=330 group A view (node-1): [1,2,3,4,5]
t=330 group B view (node-6): [6,7,8,9,10]
t=330 max lamport clock before heal: 293

=== Phase 3: heal network; first cross-group PingReq triggers merge ===
[t= 338] node-4: initiating merge handshake with 9
[t= 340] node-1: initiating merge handshake with 9
[t= 340] node-8: initiating merge handshake with 5
[t= 340] node-4: merged with 9; epoch=1 members=[1,2,3,4,5,6,7,8,9,10]
first cross-group success: ping-req (ack 9 -> 1) at t=337
merge handshake: proposals=3 accepts=1 commits=9
converged=true at t=342

=== Final state ===
  node-1: epoch=1 clock=321 state=IDLE members=[1,2,3,4,5,6,7,8,9,10]
  ...
  node-10: epoch=1 clock=328 state=IDLE members=[1,2,3,4,5,6,7,8,9,10]
clock correction: all clocks >= pre-heal max (293): true
```

## 测试结果

```
1..6
# tests 6
# pass 6
# fail 0
```

覆盖用例：

1. 分区后形成两个闭环（各组视图恰为 `[1-5]` / `[6-10]`，跨组消息全部被丢弃）；
2. 重连后首次跨组成功为 PingReq，触发合并握手，全群收敛且无节点卡在握手态（无死锁）；
3. 双方同时发起握手时按确定性裁决合并，epoch 恰好提升一次；
4. `merge-commit` 幂等（重复投递不改变状态）；
5. 合并时 Lamport 时钟被校正至不低于对侧；
6. 合并后全群时钟持续推进且漂移有界。
