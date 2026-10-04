# capability-negotiation

能力协商库：输入双方的功能声明（能力集合 + 依赖/互斥关系），求解优先级最高的共同配置；
协商结果通过转录（transcript）绑定双方原始声明，重放旧声明会被识别为另一轮；
无解时返回最小冲突集合。

## 核心概念

- `PartyDeclaration`：一方在一个协商轮次中的声明。`round` 是每轮必须刷新的随机数，
  是防重放的关键——任何字段（含 `round`）的变化都会改变声明哈希。
- `CapabilityDecl`：单个能力。`priority` 越大越优先；`requires` 声明依赖；
  `conflicts` 声明互斥；`mandatory` 表示任何可接受配置都必须包含它。
- `Transcript`：协商凭证。包含双方声明哈希、`roundId`、结果配置哈希以及绑定所有字段的
  `digest`。

## 协商语义

- 共同配置只能取自双方都声明的能力（按名字取交集）。
- 约束合并：双方声明的 `requires`/`conflicts` 取并集；任一方标记 `mandatory` 即为必选。
- 目标：在满足依赖闭包、互斥约束、必选约束的前提下，最大化双方优先级之和；
  平局时依次偏好能力数更多、名字字典序更小（保证确定性）。
- 求解器为精确分支定界（`src/solver.ts`），最坏复杂度随共同能力数指数增长，
  适用于现实规模的声明（几十个能力）。

## 无解与最小冲突集合

无可行配置时（如必选能力对方不支持、必选链依赖未共同支持的能力、必选能力互相冲突），
`negotiate` 返回 `status: 'conflict'`，其中 `conflict.constraints` 是删除极小的
不可满足核心（删去其中任意一条约束即可恢复可行），`conflict.unsupported` 列出核心闭包中
未被共同支持的能力。

## 转录校验与重放识别

```ts
const res = negotiate(declA, declB);
// 之后任意一方可校验：
const v = verifyTranscript(res.transcript, [presentedA, presentedB]);
// v.valid === false 且 v.replayed 包含某方，即该方出示的声明不属于本轮（重放/替换）。
```

声明哈希基于规范化 JSON（键排序、能力按名字排序、约束列表排序、缺省值归一），
语义相同的声明哈希相同；但旧轮次的声明（`round` 不同）哈希必然不同，
因此重放旧声明会导致 `roundId`/绑定哈希不匹配，被识别为另一轮。

## API

- `negotiate(declA, declB)` → `NegotiationResult`（`ok` 带 `config`/`score`，或 `conflict` 带最小冲突核心），均含 `transcript`。
- `verifyTranscript(transcript, declarations)` → `{ valid, replayed, errors }`。
- `transcriptBinds(transcript, decl)` / `computeRoundId(a, b)` / `hashDeclaration(decl)`。
- 底层：`buildModel`、`solveOptimal`、`minimizeConflict`、`analyzeConflict`。

## 构建与测试

需要 TypeScript 编译器（开发环境使用 3.9+ 即可）：

```sh
tsc -p tsconfig.json && node dist/test/run-tests.js
```
