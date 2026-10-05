# 导出脱敏检查器（export-desensitization-checker）

沿字段计算依赖图传播敏感级别，对导出计划做静态合规检查：
**隐藏原始字段并不能消除派生字段的泄露风险**，只有达到最小分组人数的聚合才允许降级。

## 模型

字段定义（`src/types.ts`）构成一张有向依赖图，四种节点：

| 节点 | 敏感级别规则 | 人数规则 |
| --- | --- | --- |
| `source` 原始字段 | 声明的级别 | 声明的覆盖人数 |
| `derive` 派生字段 | 取所有输入的**最高**级别 | 取输入人数最小值 |
| `filter` 过滤字段 | 不变 | 减少为 `retainedPopulation`（必须小于输入） |
| `aggregate` 聚合字段 | 最小组人数 ≥ `minGroupSize` 时降为 `downgradeTo`，否则继续传播 | 变为分组数 |

聚合的最小组人数取保守估计：`min(实测最小组人数, floor(输入人数 / 分组数))`。
因此上游过滤减少人数后，原本达标的聚合可能自动失效。

## 检查内容

`checkExportPlan(fields, plan)`（`src/checker.ts`）从导出字段出发反向求值（记忆化，
共享计算节点只求值一次），产出：

- `SENSITIVITY_EXCEEDED`：导出字段有效级别超过计划上限，附完整**违规来源链**
  （从敏感源字段到导出字段的每一步变换及说明）；
- `AGGREGATION_BELOW_MIN_GROUP`：聚合未达到最小分组人数，降级被禁止；
- `FILTER_NO_REDUCTION`：过滤声明的保留人数不小于输入人数；
- `UNKNOWN_FIELD` / `CYCLIC_DEPENDENCY`：计划引用了不存在的字段或依赖成环；
- `stats.sharedNodes`：被多个下游引用的共享计算节点。

## 使用

```ts
import { checkExportPlan, Sensitivity, FieldDef } from './src';

const fields: FieldDef[] = [
  { kind: 'source', name: 'salary', sensitivity: Sensitivity.Sensitive, population: 1000 },
  { kind: 'filter', name: 'cn_salary', input: 'salary', retainedPopulation: 120 },
  {
    kind: 'aggregate', name: 'cn_dept_avg', input: 'cn_salary',
    groupCount: 20, minGroupSize: 10, downgradeTo: Sensitivity.Internal,
  },
];

const result = checkExportPlan(fields, {
  name: 'monthly-export',
  exportFields: ['cn_dept_avg'],
  maxAllowedSensitivity: Sensitivity.Internal,
});
// floor(120/20)=6 < 10 → 降级失效，salary 的敏感级别沿过滤链传播到导出字段
```

## 构建与测试

```sh
npm run build   # tsc 编译到 dist/
npm test        # 编译并运行 test/checker.test.ts
```
