# offline-request-allocator

离线批次请求分配器。把一组不可拆分的请求分配到可用节点上，兼顾节点容量与就近访问：
不在运行时补救超载，而是在求解时按字典序直接优化全局目标。

## 目标（字典序，优先级从高到低）

1. **最大化被接纳需求总量**（被拒绝请求的需求不计入）；
2. **最小化跨区代价**（请求地区 ≠ 节点地区时，取固定代价矩阵 `costs[请求地区][节点地区]`，同区为 0）；
3. **最小化相对上一方案的迁移请求数**（仅统计出现在 `previous` 中的请求；其新落点
   —— 节点或拒绝 —— 与旧值不同即计一次迁移；`previous` 可以引用已下线的节点）；
4. **决胜**：按请求 ID 排序后的节点 ID 向量取字典序最小（拒绝记为空串，排在任何节点 ID 之前）。

该序是全序，最优解唯一，因此结果与输入数组的排列无关。

## 语义

- 请求不可拆分：每个请求整体落到某个节点或被拒绝。
- 容量变小或节点离线时重算整批：旧方案只影响第 3 级目标，不作为约束。
- 绝不超配：每个节点的已分配需求之和不超过其容量。
- 精确搜索预算：`budget.maxStates`（搜索状态数，默认 1,000,000）与 `budget.timeMs`
  （墙钟时间）。预算耗尽时返回当前最优的**可行**方案并置 `provenOptimal: false`，
  不杜撰最优性；预算内完成则为 `provenOptimal: true`。

## 输入格式

```json
{
  "requests": [{ "id": "r1", "region": "cn-north", "demand": 3 }],
  "nodes": [{ "id": "n1", "region": "cn-north", "capacity": 5 }],
  "costs": { "cn-north": { "cn-south": 2 }, "cn-south": { "cn-north": 2 } },
  "previous": { "r1": "n1" },
  "budget": { "maxStates": 100000, "timeMs": 1000 }
}
```

- `demand` / `capacity` 为非负整数；代价为有限非负数。
- 同区代价默认 0；出现的跨区地区对必须在 `costs` 中显式给出，否则报错并指明缺失的地区对。
- `previous` 可选；值为节点 ID 或 `null`（此前被拒绝）。`budget` 也可经命令行覆盖。

## 输出

`SolveResult` JSON：`assignments`（请求 → 节点或 `null`）、`rejected`（拒绝清单）、
`capacityLedger`（每节点 capacity/used/remaining 容量账本）、`migrations`（迁移计划，
含 `from`/`to`，可为 `null`）、`provenOptimal`、`stats`（搜索状态数、接纳量、跨区代价、
迁移数等）。

## 使用

```bash
npm run build                       # src/*.ts -> dist/*.js
node dist/cli.js examples/example.json  # 求解文件；省略路径或传 "-" 则从 stdin 读
node dist/cli.js - --budget 1000 --time-ms 500 --baseline --compact < input.json
```

库用法：`import { solveAllocation } from './dist/allocator.js'`，
`solveAllocation(input, { maxStates, timeMs })`。`dist/greedy.js` 提供贪心最近节点基线，
`dist/bruteforce.js` 提供穷举验证器。

## 测试

```bash
npm test
```

覆盖：≤10 请求随机实例与穷举枚举对拍、输入排列一致性、贪心最近节点导致更差接纳量、
单项需求超过所有容量、跨区代价并列决胜、旧方案节点退出与容量收缩、预算/时间中断返回
可行解并标记未证明最优、容量账本不超配、CLI 正常与报错路径。测试数据全部由
`test/util.mjs` 中的种子随机数本地合成。

## 离线构建说明

本环境无网络、无 `tsc`，因此 `src/` 使用 TypeScript 的**可擦除语法子集**编写，
由零依赖脚本 `scripts/build.mjs` 擦除类型生成 `dist/`，每个产物经 `node --check`
校验，且全部测试直接运行在 `dist/` 上。源码约定：不用 enum/namespace/class/装饰器、
不用非空断言与调用点泛型、`as` 断言仅用于表达式、类型标注不跨行。
有网环境可 `npm i -D typescript && npm run typecheck` 用 `tsconfig.json` 做严格类型检查。
