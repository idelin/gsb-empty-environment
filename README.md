# Multi-Paxos 多数派共识与无空洞日志提交引擎

TypeScript 实现的 Multi-Paxos 分布式共识日志协议：无单点故障、多数派提交、
领导者租约优化、空洞修补与少数派脏日志安全覆写。全部组件由**单节点确定性
时钟**驱动（虚拟时钟逐 tick 推进，无真实定时器、无随机源），测试使用
Node.js 内置测试运行器（`node:test`）。

## 运行环境

- Node.js **≥ 23.6**（原生 TypeScript 类型擦除，无需编译、无需任何依赖）
- 或 Node.js 22.6+：`node --experimental-strip-types ...`
- 仓库内含 `.nvmrc`（v23.9.0）

```bash
npm test        # node --test（内置测试运行器，25 个测试）
npm run demo    # 分区验收场景的运行样例
```

## 架构

| 文件 | 职责 |
| --- | --- |
| `src/ballot.ts` | 选票 `(round, nodeId)` 全序比较；节点 ID 决胜保证同选票唯一 |
| `src/types.ts` | 消息类型：Prepare/Promise、Accept/Accepted、Commit、Heartbeat/Ack |
| `src/node.ts` | 协议状态机（follower/candidate/leader），纯确定性：`tick(now)` 驱动 |
| `src/network.ts` | 确定性网络：固定 1 tick 延迟、按 `(deliverAt, seq)` 全序投递、链路切断模拟分区 |
| `src/cluster.ts` | 集群驱动器：单虚拟时钟逐 tick 推进（投递消息 → 触发超时 → 冲刷发件箱） |
| `src/invariants.ts` | 测试侧安全预言机（SafetyOracle），每 tick 校验核心不变量 |
| `examples/demo.ts` | 验收场景运行样例（5 节点分区 → 换主 → 修补 → 覆写 → 收敛） |

## 协议要点

- **Phase 1（Prepare/Promise）**：候选者以单调递增选票发起 Prepare；接受者仅在
  选票不低于已承诺选票时响应 Promise，并上报全部已接受槽位。多数派 Promise
  即当选。已授予的领导者租约内，接受者拒绝为他者投票。
- **Phase 2（Accept/Accepted）**：领导者以当选选票广播 Accept；接受者仅在选票
  不低于承诺选票时落盘并应答。槽位获多数派 Accepted 后提交。
- **领导者租约优化**：租约有效期内（心跳获多数派应答即续租），客户端写直接走
  Phase 2，连续 LogEntry 不再经过 Phase 1（测试断言选举后 Prepare 数为零增长）。
- **空洞修补**：新领导者当选后，按 Promise 法定集合的报告对每个槽位取最高选票
  值重新提议；法定集合中无人报告的槽位以 NOOP 封填；少数派脏日志因选票更低
  被安全覆写。
- **无空洞提交**：`applied` 前缀只能逐槽追加——领导者侧要求该槽获多数派应答，
  跟随者侧额外要求本地条目选票与提交领导者选票一致（防止把本地脏条目误当
  已选值提交）；落后跟随者由心跳携带的提交进度触发领导者逐槽重发修补。
- **租约与心跳**：领导者心跳获多数派应答才续租；被隔离的旧领导者租约到期自动
  退位，无法在少数派分区提交任何新条目。

## 核心不变量（每 tick 断言）

1. **状态机安全**：任意两节点同一槽位不会提交不同值（对首个观测到的提交值建账）。
2. **提交后不接受异值**：槽位一旦在某处提交，任何节点不得再于该槽接受不同值。
3. **无空洞提交**：已提交前缀在接收日志中逐槽存在且值一致，commitIndex 连续。
4. **选举安全**：同一选票至多一个领导者。
5. **严格无泄漏**：收敛后在途消息不超过一个心跳轮次，所有节点发件箱排空。

## 测试覆盖（25 个测试，全部通过）

- `test/ballot.test.ts`：选票全序、相等性、格式化。
- `test/acceptor.test.ts`：Promise 携带已接受条目、过期 Prepare/Accept 拒绝、
  高选票覆写未提交条目、已提交槽位不可变、租约屏蔽外部 Prepare、空洞不跳过。
- `test/election.test.ts`：单领导者当选、少数派永远无法当选、多数派承诺要求、
  高选票领导者罢黜旧领导者。
- `test/lease-commit.test.ts`：租约下绕过 Phase 1 连续提交、无多数派不提交、
  租约到期换主、心跳续租、非领导者拒写。
- `test/hole-repair.test.ts`：新领导者 Phase 1 填补接受日志空洞；法定集合已知
  空洞以 NOOP 封填并覆写少数派脏条目。
- `test/partition.test.ts`：**验收场景**——5 节点分区，旧 Leader 被多数派新
  Leader 取代，Phase 1 补全未提交槽位并安全覆写少数派脏日志，愈合后全集群收敛。
- `test/fuzz.test.ts`：种子化随机分区/写入交错（每 tick 校验全部不变量），
  愈合后收敛性与无泄漏断言；同种子重放状态逐比特一致（确定性）。

## 测试结果

```
ℹ tests 25
ℹ suites 0
ℹ pass 25
ℹ fail 0
```

另以 60 个随机种子做压力验证（每个种子 1500 tick 随机分区+写入、500 tick
愈合收敛，每 tick 校验不变量）：全部无安全违规、无发散、无泄漏。
