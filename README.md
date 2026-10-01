# Graceful Leave & Quiescent Period Broadcast (TypeScript)

节点主动下线协议：节点主动发起 Leave 并广播离开意向；集群将其置为 `Leaving`
状态，在平静期（Quiescent Period）内阻断新流量、放行在途请求；平静期定时器
超时后，各节点物理销毁该节点的路由条目。全程零依赖，仅使用 Node.js 内置模块
（`node:test` / `node:events`），Node >= 22.6 原生运行 TypeScript。

## 协议状态机

```
节点生命周期:   active --initiateLeave()--> leaving --平静期超时--> gone
路由条目状态:   ready  --收到 leave-intent--> leaving --定时器超时--> removed (物理销毁)
```

- `leaving`（平静期）：新流量在路由准入处被**立即**拒绝
  （`NodeDrainingError` / `ERR_NODE_DRAINING`），不悬挂、不超时；
  已进入在途（in-flight）的请求继续执行直至完成（平滑排水）。
- `removed`：路由条目被物理删除，后续请求立即失败
  （`NodeNotFoundError` / `ERR_NODE_NOT_FOUND`），同样无 RPC 超时。

## 目录结构

- `src/types.ts` — 生命周期与广播消息类型（`leave-intent` / `leave-complete`）
- `src/errors.ts` — 结构化即时错误：`NodeDrainingError`、`NodeNotFoundError`
- `src/messageBus.ts` — 同步有序的集群广播总线
- `src/routingPool.ts` — 每节点本地路由视图：准入检查、在途计数、平静期定时器、物理驱逐
- `src/node.ts` — 集群节点：`initiateLeave()`、RPC 收发、广播处理
- `src/cluster.ts` — 集群装配、RPC 入口、可观测事件（`leaving` / `removed` / `gone`）
- `test/graceful-leave.test.ts` — Node 内置测试（`node:test` + mock timers）
- `examples/demo.ts` — 端到端运行样例

## 运行

```bash
npm test       # 运行全部测试（node --test）
npm run demo   # 运行演示样例
```

## 测试结果

```
✔ 节点发起 Leave：进入 Leaving 状态并广播离开意向
✔ 重复 Leave 幂等：只广播一次离开意向
✔ 其他节点收到广播后平滑将其移出就绪路由池
✔ 平静期内新流量被立即阻断：结构化错误而非 RPC 超时
✔ 平静期内进行中的请求正常完成（平滑排水，不中断在途流量）
✔ 平静期定时器超时后物理销毁路由条目，节点彻底下线
✔ 离开全过程无突发 RPC 超时：结果只有成功或即时的结构化错误
ℹ tests 7
ℹ pass 7
ℹ fail 0
```

## 运行样例（`npm run demo`，平静期 2000ms）

```
=== 阶段 1: 正常流量 ===
t+ 1203ms  RPC node-a -> node-c 成功: {"servedBy":"node-c","payload":"req-1"}
=== 阶段 2: node-c 主动发起 Leave 并广播离开意向 ===
t+ 1353ms  [node-a] 收到离开广播 -> 将 node-c 移出就绪路由池（进入平静期）
t+ 1354ms  [node-b] 收到离开广播 -> 将 node-c 移出就绪路由池（进入平静期）
t+ 1354ms  [node-d] 收到离开广播 -> 将 node-c 移出就绪路由池（进入平静期）
t+ 1354ms  [node-c] 已广播 leave-intent，进入 Leaving 状态
--- 平静期内：新流量被阻断，在途请求继续排水 ---
t+ 1354ms  RPC node-b -> node-c 被即时拒绝 [ERR_NODE_DRAINING]（无超时等待）
t+ 1354ms  [node-b] 就绪池重路由到 node-a
t+ 1354ms  RPC node-b -> node-a 成功: {"echoed":"rerouted-req","servedBy":"node-a"}
t+ 1354ms  等待在途慢请求完成...
t+ 2405ms  在途请求结果: {"servedBy":"node-c","payload":"slow-inflight-req"}
=== 阶段 3: 等待平静期 (2000ms) 定时器超时 ===
t+ 3355ms  [node-a] 平静期结束 -> 物理销毁 node-c 的路由条目
t+ 3356ms  [node-b] 平静期结束 -> 物理销毁 node-c 的路由条目
t+ 3356ms  [node-d] 平静期结束 -> 物理销毁 node-c 的路由条目
t+ 3356ms  [cluster] node-c 已彻底下线 (gone)
--- 物理移除后：路由条目已销毁 ---
t+ 4608ms  RPC node-b -> node-c 被即时拒绝 [ERR_NODE_NOT_FOUND]（无超时等待）
=== 最终路由表 ===
  [node-a] ready -> node-b, node-d
  [node-b] ready -> node-a, node-d
  [node-d] ready -> node-a, node-b
  [node-c] lifecycle -> gone
```

样例要点：1.2s 的慢请求在 Leave 发起前进入在途，平静期内不受新流量阻断
影响，完整执行至 t+2405ms 成功返回；平静期（2000ms）到期后三个对等节点
同时物理销毁路由条目；全程所有失败均为即时结构化错误，无任何 RPC 超时。
