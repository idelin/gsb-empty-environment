# 带标签与元数据属性的异构节点成员列表路由 (TypeScript)

基于 Node.js 内置模块（`node:test` + 原生 TS 类型擦除，Node ≥ 22.18 / 23.6），零依赖实现：

- **异构节点成员列表**：每个节点携带动态元数据 —— 机房 `zone`、负载权重 `weight`、角色 `role`，以及任意扩展 `labels`。
- **并发更新与版本仲裁**：元数据记录携带 `(version, updatedBy)`，版本号采用 Lamport 时钟；冲突时高版本获胜，同版本按 `updatedBy` 字典序确定性收敛（LWW）。
- **Gossip 元数据同步**：`digest()` 交换版本向量 + 双向差异推送（反熵），多跳传播后全集群收敛。
- **动态拓扑子集筛选与路由**：按属性（`zone` / `role` / `labels.*` / `weight`）筛选健康节点子集，路由返回权重最高者（并列按节点 ID 确定性选择）。

## 目录结构

```
src/types.ts        # 元数据、版本记录、筛选条件等类型定义
src/memberlist.ts   # 成员列表：版本仲裁 + Gossip 同步
src/router.ts       # 属性筛选器 + 权重优先路由
test/memberlist.test.ts  # node:test 测试
examples/demo.ts    # 端到端运行样例
```

## 运行

```bash
npm test        # node --test（自动发现 test/*.test.ts）
npm run demo    # node examples/demo.ts
```

## 运行样例输出

场景：三节点集群，n1 负载权重由 10 动态调整为 80，经 Gossip 同步后，
路由查询 `zone=us-east` 返回最新健康且权重最高的节点。

```
[1] 初始路由 zone=us-east -> n2 (weight=50)
[2] n1 权重更新 10 -> 80 (version=4)
[3] Gossip 同步完成，各节点视图中的 n1.weight = 80, 80, 80
[4] 节点 a 路由 zone=us-east -> n1 (weight=80)
[4] 节点 b 路由 zone=us-east -> n1 (weight=80)
[4] 节点 c 路由 zone=us-east -> n1 (weight=80)
[5] n1 宕机后，节点 b 路由 zone=us-east -> n2
```

## 测试结果

```
✔ MemberList 元数据与版本仲裁
  ✔ 本地更新元数据：权重 10 -> 80，版本号单调递增
  ✔ 并发更新仲裁：高版本获胜，同版本按 updatedBy 字典序收敛
  ✔ Gossip 同步：权重 10 -> 80 传播到所有节点
  ✔ 健康状态同步：dead 节点被排除
✔ Router 属性筛选与路由
  ✔ zone=us-east 返回健康且权重最高的节点
  ✔ 权重最高者不健康时，降级到次优健康节点
  ✔ 异构标签筛选：labels.tier + zone 组合
ℹ pass 7  ℹ fail 0
```

## API 速览

```ts
import { MemberList } from './src/memberlist.ts';
import { Router } from './src/router.ts';

const ml = new MemberList('n1');
ml.addNode('n1', '10.0.0.1:7000', { zone: 'us-east', weight: 10, role: 'leader', labels: { tier: 'gold' } });
ml.updateMetadata('n1', { weight: 80 });        // 动态更新，版本号仲裁
ml.gossipWith(peerList);                         // Gossip 同步

const router = new Router(ml);
router.select({ zone: 'us-east', 'labels.tier': 'gold' }); // 拓扑子集筛选
router.route({ zone: 'us-east' });               // 健康且权重最高的节点
```
