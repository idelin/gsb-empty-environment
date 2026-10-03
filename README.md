# 异构节点成员列表路由（TypeScript）

带标签与元数据属性的集群成员列表 + 属性路由。支持节点动态元数据（机房 Zone、
权重 Weight、角色 Role）、元数据并发更新的版本号仲裁、Gossip 反熵同步，以及
基于属性的动态拓扑子集筛选。

## 环境要求

- Node.js >= 22.6（原生运行 TypeScript，无需编译；推荐 >= 23.6，类型擦除默认开启）
- 测试使用 Node.js 内置 `node:test`，零依赖

## 运行

```bash
npm test        # node --test（内置测试运行器）
npm run demo    # node examples/demo.ts（运行样例）
```

## 结构

- `src/types.ts` — 元数据/成员记录/gossip 传输格式定义
- `src/member-list.ts` — 成员列表：本地元数据更新、版本仲裁、心跳健康判定
- `src/gossip.ts` — 进程内 Gossip 集群模拟：push-pull 反熵、收敛检测
- `src/filter.ts` — 属性筛选表达式解析与匹配（`zone=us-east,weight>=50,role!=leader`）
- `src/router.ts` — 路由器：筛选健康子集并按权重择优
- `examples/demo.ts` — 端到端样例
- `test/` — `node:test` 单元与验收测试

## 并发仲裁规则

1. 元数据版本号（Lamport 风格，属主节点单调递增）大者获胜；
2. 同版本并发冲突时，比较元数据规范化摘要，字典序大者获胜 —— 与到达顺序无关，全网确定性收敛；
3. 属主节点对自身记录拥有最终权威（SWIM 式自辩），忽略 gossip 中关于自己的记录；
4. 心跳取最大值；故障状态按 `dead > suspect > alive` 严重程度传播。

## 验收边界

动态更新某节点负载权重 10 -> 80，各节点经 Gossip 同步元数据；路由查询
`route('zone=us-east')` 返回最新健康且权重最高的节点。对应测试见
`test/router.test.ts` 的「验收边界」套件。
