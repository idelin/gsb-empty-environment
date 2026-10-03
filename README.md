# 双时态合同履约状态机

TypeScript 实现，零依赖，使用 Node.js 内置测试运行器（Node ≥ 23.6 原生支持类型擦除，直接运行 `.ts`）。

## 模型

- **业务时间 (valid time)**：事件业务上发生的时间；合同状态沿此轴演进：
  `DRAFT 草案 → EFFECTIVE 生效 → IN_PROGRESS 履行中 → TERMINATED 终止`（到达业务期终点即终止）。
- **知悉时间 (transaction time)**：系统录入/获知事件的时间。每条交付凭证携带
  `[recordedAt, voidedAt)` 的知悉区间，支持**补交**（以过去的业务时间录入）与**作废**（自某知悉时间起不可见）。
- **双时态切片**：`snapshot(validTime, knowledgeTime)` 实时返回该切片下的状态、
  履约完成度百分比（可见交付数量之和 / 约定总量，封顶 100%）与可见凭证列表。

## API（src/contract.ts）

| 方法 | 说明 |
| --- | --- |
| `recordDelivery(knowledgeTime, validTime, quantity)` | 录入交付凭证（可补交历史业务时间） |
| `voidDelivery(knowledgeTime, deliveryId)` | 作废凭证，仅影响该知悉时间之后的查询 |
| `stateAt(validTime, knowledgeTime)` | 切片下的合同状态 |
| `completionAt(validTime, knowledgeTime)` | 切片下的完成度百分比 [0, 100] |
| `snapshot(validTime, knowledgeTime)` | 状态 + 完成度 + 可见凭证的完整快照 |

## 运行

```sh
npm test    # node --test，运行 test/contract.test.ts
npm run demo  # 运行 examples/demo.ts
```

## 验收场景（examples/demo.ts 输出）

合同业务期 `[1, 100)`、约定总量 100；知悉 10 录入业务 50 交付 30；
知悉 20 作废该交付并录入业务 40 交付 60。查询业务时间 60：

```
知悉 5 | 状态=EFFECTIVE   完成度=0%  可见交付=[]
知悉15 | 状态=IN_PROGRESS 完成度=30% 可见交付=[业务50×30]
知悉25 | 状态=IN_PROGRESS 完成度=60% 可见交付=[业务40×60]
```

状态机沿业务时间演进（固定知悉 25）：业务 0=DRAFT，1~39=EFFECTIVE，40~99=IN_PROGRESS，100=TERMINATED。

## 测试结果

`node --test`：10 个测试全部通过（验收切片对比、状态机演进、补交改写历史切片、
作废的知悉时间语义、完成度封顶、非法输入拒绝）。
