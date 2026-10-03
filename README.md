# Multi-Paxos 多数派共识与无空洞日志提交引擎 + 形式化约束巡检与不变式断言仪

TypeScript 实现的无单点故障分布式共识日志协议，附带全量状态离线完整性巡检仪与
在线不变式断言仪。纯 Node.js 内置模块（`node:test` + 原生类型擦除），零依赖。

## 运行要求

- Node.js **>= 22.6**（使用 `--experimental-strip-types` 原生运行 TypeScript；
  Node >= 23.6 下类型擦除默认开启）。无需 `npm install`，无任何第三方依赖。

```bash
npm test          # 运行全部测试（node --test）
npm run demo      # 运行分区-恢复全流程样例
# 或显式：
node --experimental-strip-types --test tests/*.test.ts
node --experimental-strip-types examples/demo.ts
```

## 架构

| 文件 | 职责 |
| --- | --- |
| `src/types.ts` | 选票（Ballot）、日志条目（LogEntry）、消息与事件类型 |
| `src/node.ts` | Multi-Paxos 节点：Prepare/Promise、Accept/Accepted、租约、空洞修补 |
| `src/network.ts` | 确定性模拟网络：分区、丢包、显式消息投递 |
| `src/cluster.ts` | 集群编排、逻辑时钟、快照、严格无泄漏断言 |
| `src/inspector.ts` | 离线全量状态完整性巡检（纯函数） |
| `src/monitor.ts` | 在线不变式断言仪：每条消息投递后全量校验 |

### 共识引擎（`src/node.ts`）

- **两阶段优化**：Phase 1（Prepare/Promise）选主并修复未提交后缀；Phase 2
  （Accept/Accepted）将条目锚定在新选票下。
- **领导者租约**：租约有效期内的客户端提案直接走 Phase 2，连续提交
  LogEntry，完全绕过 Phase 1；每次成功提交自动续租。
- **无空洞提交**：`commitIndex` 只在连续已选（chosen）前缀上推进；
  乱序到达的提交通知先缓存、按序生效。
- **空洞修补**：新 Leader 在 Phase 1 汇总多数派的接受状态，对每个开放槽位
  采纳最高选票的值，无人持有的槽位填 no-op，再统一重新 Accept。
- **安全覆写**：已提交记录永不覆写；少数派脏日志（未达多数的接受记录）
  在收到更高选票 Accept 或提交通知时被安全替换。
- **心跳再同步**：Leader 周期心跳携带 `commitIndex`，落后 Follower 发起
  `sync-request` 补齐缺失的已提交槽位（分区愈合后收敛）。

### 形式化约束巡检仪（`inspect()`，离线、纯函数）

对集群全量快照主动发现：

- **悬挂引用**：父槽位缺失、非连续父引用、提交指针指向未提交槽位、
  指向未知节点的 Leader 引用、租约已过期却仍自称 Leader。
- **孤立环路**：日志父引用链中与创世点断开的环；无真实 Leader 的
  领导权信念环（A→B→C→A）。
- **数据倾斜**：日志规模比/绝对滞后超阈值、已提交槽位副本数低于多数派、
  未提交脏条目在单节点过度集中。
- **不变式**：I1 选举安全（按选票）、I2 日志匹配、I3 状态机安全、
  I4 无空洞提交、I5 领导者完备性。

### 不变式断言仪（`InvariantMonitor`，在线）

挂载到活集群上，每条消息投递后、每个集群事件后重新校验全部安全不变式，
在违规发生的精确状态转移处捕获；`assertClean()` 以异常形式抛出全部违例。

## 验收场景

`tests/partition.test.ts`：5 节点集群，{A,B}|{C,D,E} 分区 → 旧 Leader A
被多数派新 Leader C 取代 → C 经 Phase 1 补全未提交槽位 → 愈合后心跳再同步，
少数派脏日志被安全覆写，全集群收敛到同一条无空洞已提交前缀。

## 测试

- `tests/paxos.test.ts` — 选主、租约绕过 Phase 1、无空洞提交、租约过期、
  过期选票 nack 回退、客户端队列排空、已提交值跨任期存活
- `tests/partition.test.ts` — 验收边界：分区、取代、修补、覆写、split-brain
- `tests/gaps.test.ts` — 空洞修补（no-op 填充 + 杂散接受采纳）、乱序提交、
  缺洞 Follower 再同步
- `tests/inspector.test.ts` — 巡检仪全部边界条件（20 例，含阈值边界）
- `tests/monitor.test.ts` — 断言仪在精确转移处捕获 I1/I3/I4 违例
- `tests/concurrency.test.ts` — 种子化随机交错（选举/提案/分区/愈合/丢包/
  时钟并发混合），每次投递后校验不变式，最终收敛 + 无泄漏
- `tests/leak.test.ts` — 严格无泄漏：无未投递消息、无悬挂选举/接受轮次、
  无残留监听器、消息账目平衡
