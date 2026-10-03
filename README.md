# 网络分区自愈合并协调器（Network Partition Merging Coordinator）

裂脑恢复合并引擎（TypeScript / Node.js）。集群被网络分裂为两个孤立子网、各自形成闭环并独立演化后，
当跨子网心跳首次打通（首个成功的跨子网 **PingReq**）时，自动触发**合并握手协议**，
安全执行拓扑融合与 Lamport 时钟校正，全程无死锁。

## 架构

| 文件 | 职责 |
| --- | --- |
| `src/merge-coordinator.ts` | 核心：三阶段合并握手状态机（PROPOSE → ACCEPT/REJECT → COMMIT/DONE/FINAL） |
| `src/node.ts` | SWIM 风格节点：环探测、间接 PingReq 探测、suspect/dead、死亡节点重接 |
| `src/membership.ts` | 成员视图与两套确定性冲突规则（gossip 保守 / merge 乐观） |
| `src/network.ts` | 确定性网络模拟器：单 tick 延迟投递、`partition()` / `heal()` |
| `src/cluster.ts` | 模拟驱动器 + 死锁看门狗（有界 tick 内必须收敛） |
| `src/lamport.ts` | Lamport 逻辑时钟（接收时 `max(local, remote) + 1` 校正） |

## 协议要点

- **裂脑检测**：域标识 `P<minAliveId>` 由存活集合确定性导出；收到“对端在我视图中非存活且域标识不同”
  的报文即判定为跨分区接触。
- **触发点**：愈合后，对死亡节点的重接探测（PingReq 经中介间接 Ping）首次成功，触发合并握手。
- **无死锁**：
  - 域优先级为全序（存活数多者优先，并列时 minAlive 小者优先），同时提案必然收敛到唯一胜者，
    低优先级方放弃并 ACCEPT，高优先级方 REJECT 并继续 —— 不存在互相等待；
  - 每个阶段超时 + 有界重试，耗尽回退 idle，可被后续心跳重新触发；
  - COMMIT 幂等，重复提交只应用最小 committer 的视图。
- **时钟校正**：所有报文携带 Lamport 时钟；COMMIT 额外携带 `clockFloor = max(双方时钟)`，
  合并后全员时钟推进到分裂前最大值之上。
- **防视图污染**：合并视图加盖“合并纪元” incarnation，使提交前在途的旧摘要在 gossip 中必然落败；
  异域 digest 一律不采纳，只能经握手安装合并视图。

## 运行

需要 Node.js >= 23.6（原生 TypeScript 类型擦除，无需编译）。如需 `tsc` 类型检查：
`npm install` 后执行 `npm run typecheck`。

```bash
npm test        # node --test "test/*.test.ts"
npm run demo    # node examples/demo.ts
```

## 运行样例（`npm run demo`，节选）

```
== Phase 2: 网络分区 [1-5] | [6-10]，各自独立演化 100 ticks ==
node | domain | alive | ring-next | clock | merge
   1 | P1     |     5 |         2 |   448 | idle
   5 | P1     |     5 |         1 |   452 | idle      <- 闭环回绕
   6 | P6     |     5 |         7 |   419 | idle
  10 | P6     |     5 |         6 |   417 | idle      <- 闭环回绕
分区期间丢包数: 179

== Phase 3: 网络愈合，等待首个跨子网 PingReq 触发合并握手 ==
愈合后 6 ticks 内完成合并（无死锁，看门狗上限 300）
时钟校正: 愈合前最大时钟=452, 合并后最小时钟=482

--- 关键协议事件 ---
tick=110 node= 6 foreign-contact via=PING_REQ peer=5 remote=P1   <- 首次打通：PingReq 路径
tick=110 node= 6 propose         ->5
tick=111 node= 5 reject          remote=P6 from=6                <- 同时提案，优先级裁决
tick=111 node= 5 propose         ->6
tick=112 node= 6 accept          remote=P1 from=5
tick=113 node= 5 commit          members=10 domain=P1
tick=114 node= 2 commit-applied  from=5 domain=P1
...
tick=115 node= 1 final           done=10

--- 合并完成：单一域、视图一致、时钟已校正 ---
   1..10 | P1 | alive=10 | merge=idle
```

## 测试结果（`npm test`，Node 内置 `node:test`）

```
✔ 分区后形成两个封闭子网环，域标识分裂
✔ 重连后首个跨子网 PingReq 成功触发合并握手并无死锁收敛
✔ 同时提案由确定性优先级裁决（出现 reject 且不死锁）
✔ 合并后拓扑融合：全员 10 节点存活、视图逐字节一致、单一域
✔ 时钟校正：合并后所有节点时钟越过分裂前最大值且保持单调
✔ 域优先级是全序：同时提案必有唯一胜者
✔ 视图合并规则：incarnation 高者胜，并列时存活优先，自身永不判死
ℹ tests 7  ℹ pass 7  ℹ fail 0
```
