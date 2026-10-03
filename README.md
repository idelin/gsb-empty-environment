# Late Materialization Projection Operator

OLAP 分析型查询的延迟物化投影算子（TypeScript 实现，零依赖）。

## 核心思想

对 `SELECT * WHERE a > 10 ORDER BY b LIMIT 10` 这类查询，传统执行器会在查询初期
把全部 100 列物化进内存。本算子将物化推迟到排序与分页收敛之后：

1. **FILTER** — 仅全列扫描过滤列 `a`，生成选择向量（物理行号列表）；
2. **ORDER BY** — 仅全列扫描排序列 `b`，对选择向量内的行号排序；
3. **LIMIT** — 截断出最终胜出的 top-k 物理行号；
4. **LATE MATERIALIZE** — 此时才对其余 98 个昂贵列（大字符串）按胜出行号做点查。

100 列 × 10,000 行的表上：eager 基线读取 1,000,000 个单元格，本算子仅读取
21,000 个（2 次全列扫描 + 100 列 × 10 行点查），**节省 97.9% IO**（验收标准 ≥90%）。

## 目录结构

- `src/column-store.ts` — 列式存储引擎，内置 IO 统计（全列扫描 / 点查分别计数）
- `src/query.ts` — 查询计划模型 + SQL 子集解析器
- `src/late-materialization.ts` — 延迟物化执行器 + eager 基线执行器（对照用）
- `src/sample-data.ts` — 确定性 100 列样例数据生成器
- `tests/late-materialization.test.ts` — Node.js 内置测试（`node:test`）
- `examples/demo.ts` — 可运行样例，打印 IO 对比与 top-10 结果

## 运行

需要 Node.js ≥ 23.6（原生支持 TypeScript 类型擦除；22.6+ 需加
`--experimental-strip-types`）。无任何第三方依赖，无需 `npm install`。

```bash
npm test        # 运行 node:test 测试（7 个用例）
npm run demo    # 运行样例，输出 IO 对比
```

## 测试覆盖

- SQL 解析为正确的查询计划；
- 前中期仅全列扫描 `a`、`b` 两列（扫描日志断言）；
- 98 个昂贵列只对 10 个胜出行做点查，且无任何昂贵列被全列扫描；
- 相对 eager 基线节省 ≥90% IO；
- 与 eager 执行结果逐行、逐单元格完全一致（正确性）；
- 结果行包含全部 100 列且排序正确；
- 无过滤 / 无排序 / DESC 等边界形态。
