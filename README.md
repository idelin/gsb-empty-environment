# lease-quorum-arbiter

多节点租约竞争与脑裂预防仲裁器：基于多数派（Quorum）租约授权的主从选举协议，
TypeScript 实现，Node.js 内置测试运行器（`node:test`）验证，零依赖。

## 协议设计

- **多数派租约授权**：候选者必须获得 `floor(n/2)+1` 个节点（含自身）的租约授权
  才能成为 Leader；Leader 只有在租约未过期时才响应写请求。
- **租约互斥（安全核心）**：每个投票节点同一时刻只把租约授权给一个持有者；
  授权未过期前，即使收到更高 term 的请求也拒绝再授权给他人。任意两个多数派
  必然相交，因此任意时刻全集群至多存在一个合法 Leader —— 杜绝脑裂。
- **租约续订**：Leader 在租约剩余一半时向多数派续订；续订失败（如被分区）
  不延长租约，到期自动降级为 Follower 并拒写。
- **预投票（Pre-Vote）**：正式选举前先进行无副作用的预投票，避免被分区节点
  以更高的 term 反复打扰健康多数派（防止非对称分区下的活锁）。
- **防护令牌（Fencing Token）**：写响应携带 Leader 的 term，可作为下游存储的
  防护令牌，拒绝过期 Leader 的迟滞写入。

## 环境要求

- Node.js ≥ 23.6（直接运行 TypeScript，无需编译；仓库内 `.nvmrc` 锁定 v23.9.0）

## 运行

```bash
nvm use            # 或确保 node >= 23.6
npm test           # node --test，运行全部测试
npm run demo       # 运行 5 节点 2/3 分区演示样例
```

## 验收场景（test/partition.test.ts）

5 节点集群被划分为 2 节点与 3 节点两个隔离分区：

1. `node-0` 经多数派授权成为 Leader（term 1，租约至 t=1000ms）。
2. 对称分区 `{node-0,node-1} | {node-2,node-3,node-4}`。
3. 少数派 Leader 续订仅得 2 票 < Quorum 3，租约于 t=1000ms 过期后自动降级，
   写请求被拒绝（`write rejected`）。
4. 多数派分区在 t=1700ms 选出新 Leader（term 2），继续续订并服务写请求。
5. 全程逐 tick 断言 `servingLeaders().length <= 1`（test/split-brain.test.ts），
   并覆盖非对称分区（单向丢包）与分区愈合后的收敛。

## 目录结构

```
src/types.ts    消息与租约类型
src/clock.ts    可注入时钟（确定性仿真）
src/network.ts  同步消息总线，支持对称/非对称（有向）链路故障
src/node.ts     节点：预投票、选举、租约授权/续订、写服务
src/cluster.ts  集群仿真框架（时钟推进、分区、愈合）
examples/partition-demo.ts  验收场景演示
test/           node:test 测试（选举、分区、脑裂不变量）
```
