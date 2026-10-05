# sparse-pivot-table

稀疏透视表库（TypeScript）：多级行列维度、小计/总计、指标可撤回、空值维度独立标签、稳定排序。

## 特性

- **多级行列维度**：`rows` / `columns` 接受任意层数的维度字段，按层级构成维度树。
- **小计与总计**：每个非叶子层级自动生成小计行/列，末尾追加总计行/列。
- **指标可撤回**：`removeRecord(id)` 撤回记录，聚合值同步回退；某条维度路径上的最后一条记录被删除后，该路径会从维度树中剪枝清理。
- **空值维度标签**：`null` / `undefined` / 空字符串统一归入 `blankLabel`（默认 `(空)`，可自定义）。
- **稳定排序**：`sorts` 按维度字段配置 `'asc'` / `'desc'` / 自定义比较器；排序为稳定归并排序，相等键保持插入顺序。同名字段可用于不同层级，互不影响。
- **同名维度/成员不串层**：维度路径按层级位置编码，同名字段在不同层级、相同成员值在不同层级均独立聚合。
- **正确的平均数**：`avg` 聚合在节点上保存 `sum` 与 `count`，小计/总计的平均 = 总和 ÷ 总计数，而不是对小计平均数再平均。
- **重复记录**：相同维度取值的记录聚合到同一单元格，逐条撤回。

## 聚合方式

`sum` / `count` / `avg` / `min` / `max`。非数值或缺失的度量值会被跳过（`count` 统计的是有效数值个数）。`min` / `max` 内部维护值的多重计数，因此撤回记录后仍能给出正确的极值。

## 用法

```ts
import { PivotTable } from './src/index.js';

const pivot = new PivotTable({
  rows: ['region', 'city'],
  columns: ['year', 'quarter'],
  measures: [{ field: 'sales', aggregation: 'sum' }],
  sorts: { year: 'desc' },
});

pivot.addRecord({ id: 1, region: 'East', city: 'Hangzhou', year: '2024', quarter: 'Q1', sales: 10 });
pivot.removeRecord(1);

const result = pivot.getResult();
// result.rows[i].headers  —— 稀疏行头（重复分组标签为 null，便于合并单元格渲染）
// result.rows[i].cells    —— 与 result.columns 对齐的单元格，每格一个指标值数组
// result.columns          —— 叶子列 + 小计列 + 总计列
// result.grandTotal       —— 各指标的总计值
```

## 脚本

- `npm test` —— 擦除类型到 `build/` 并运行测试（无需安装依赖）。
- `npm run typecheck` —— 需要 `npm install` 安装 TypeScript 后执行 `tsc --noEmit`。

说明：仓库自带 `scripts/strip-types.mjs`，仅做类型擦除（当前环境无网络/无 tsc），
源码即标准 TypeScript，可直接用 `tsc` 编译。
