# Seed Bootstrap Discovery (TypeScript)

基于种子节点（Seed Nodes）的新节点冷启动自发现组件：新集群组网与孤儿节点入网握手器。

节点启动时向配置的静态 Seed 列表发起 **Bootstrap 握手**，拉取初始成员快照，
随后同步 **Gossip 洗牌环** 并立即接入常规格网，通过周期性洗牌交换成员样本实现全网收敛。

## 环境要求

- Node.js ≥ 22.6（利用内置 TypeScript 类型擦除直接运行 `.ts`，无需编译；开发环境为 v23.9）
- 零第三方依赖，测试使用 Node.js 内置测试运行器（`node:test`）

## 架构

```
src/
├── types.ts         # NodeMeta 与 BOOTSTRAP / SHUFFLE 协议消息定义
├── transport.ts     # Transport 抽象 + InMemoryNetwork 仿真网络（可替换为 TCP/HTTP）
├── shuffle-ring.ts  # Gossip 洗牌环（Fisher–Yates 洗牌，走完一圈自动重洗）
└── node.ts          # ClusterNode：Bootstrap 握手器 + Gossip 循环
test/bootstrap.test.ts  # node:test 验收测试
examples/demo.ts        # 运行样例：50 节点集群 + 3 个全新节点冷启动
```

### 工作流程

1. **新集群组网**：`isSeed: true` 的节点独立启动即成环，自身为初始成员。
2. **Bootstrap 握手**：非 Seed 节点向静态 Seed 列表轮询发送 `BOOTSTRAP`，
   收到 `BOOTSTRAP_ACK` 后合并全网成员快照；Seed 不可达时按
   `bootstrapRetryMs` 重试，超过 `bootstrapTimeoutMs` 报错（孤儿节点保护）。
3. **融入格网**：快照合并后立即同步洗牌环并启动 Gossip 循环，
   每轮与环上下一节点互换 `SHUFFLE` 样本；心跳单调递增保证合并新鲜度，
   对端不可达时将其移出成员表（轻量失败检测）。

## 运行

```bash
npm test      # 运行全部测试（node --test）
npm run demo  # 运行样例
```

## 运行样例（`npm run demo`）

```
[seed] node-0 上线，新集群组网完成
[cluster] 50 节点集群就绪，seed 已知成员数 = 50
[bootstrap] 3 个全新节点完成 Bootstrap 握手，耗时 0.2ms（上限 1000ms）
  fresh-1: 已知成员 51 个, 洗牌环规模 50, gossip=true
  fresh-2: 已知成员 52 个, 洗牌环规模 51, gossip=true
  fresh-3: 已知成员 53 个, 洗牌环规模 52, gossip=true
[gossip] 500ms 后新节点在既有 50 节点中的传播情况：
  fresh-1 已被 37/50 个既有节点发现
  fresh-2 已被 35/50 个既有节点发现
  fresh-3 已被 40/50 个既有节点发现
[demo] 全部节点已停止
```

## 测试结果（`npm test`，6/6 通过）

```
✔ 新集群组网：Seed 节点独立启动即成环
✔ 验收：3 个全新节点仅配置单点 Seed，1 秒内拉取 50 节点元数据并融入洗牌环
✔ 孤儿节点入网：Seed 未就绪时持续重试，Seed 上线后自动完成握手
✔ 孤儿节点入网：所有 Seed 均不可达且超时后报错
✔ Seed 故障转移：跳过不可达 Seed，使用列表中的下一个
✔ Gossip 洗牌环：节点下线后被对端移出成员表
ℹ tests 6  ℹ pass 6  ℹ fail 0
```

### 验收边界对照

| 验收项 | 结果 |
| --- | --- |
| 3 个全新节点仅配置单点 Seed 地址 | `seeds: ['node-0']`，见验收测试与样例 |
| 1 秒内通过 Bootstrap 拉取全网 50 节点元数据 | 实测握手耗时 < 1ms（测试断言 `< 1000ms`），每节点已知成员 ≥ 50 |
| 立即融入 Gossip 洗牌环 | 握手完成即 `bootstrapped=true`、洗牌环规模 ≥ 49、Gossip 循环已启动；500ms 内新节点被多数既有节点发现 |
