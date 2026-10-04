# sparse-pivot

稀疏透视表库（TypeScript）。

## 特性

- **多级行列维度**：`rows` / `columns` 按声明顺序构成层级路径，只稀疏地保存出现过的路径。
- **小计与总计**：每个行分组输出小计行，末尾输出总计行；列方向输出总计列。
- **指标可撤回**：`addRecord` 返回记录 id，`retractRecord(id)` 精确撤销该记录对所有聚合单元格的贡献。
- **空值独立标签**：`null` / `undefined` 维度值归入 `nullLabel`（默认 `(空)`，可自定义），排序时恒排最后。
- **稳定排序**：按标签或按指标值排序，相等时保持插入顺序（显式下标决胜，不依赖引擎稳定性）。
- **维度路径清理**：撤回最后一条记录后，对应维度节点与单元格被完整剪除。
- **同名不同层级**：节点键包含层级号，同一标签出现在不同层级不会混淆。
- **平均数口径**：`avg` 由 `sum / count` 计算，总计/小计不会直接平均子级平均数。

## 用法

```ts
import { PivotTable } from './src/pivot';

const pt = new PivotTable({
  rows: ['region', 'city'],
  columns: ['year'],
  measures: [
    { field: 'sales', agg: 'sum' },
    { field: 'sales', agg: 'avg' },
    { field: 'sales', agg: 'count' },
  ],
  nullLabel: '(空)',
  rowSort: { by: 'measure', direction: 'desc', measureIndex: 0 },
});

const id = pt.addRecord({ region: '华东', city: '上海', year: 2024, sales: 100 });
pt.retractRecord(id);

const view = pt.render(); // { measures, columns, rows }
```

## 构建与测试

```sh
npm run build   # tsc（本环境无网络，dist/pivot.mjs 为手工同步的等价构建）
npm test        # node test/pivot.test.mjs
```

`src/pivot.ts` 为唯一源码；`dist/pivot.mjs` 是去除类型标注后的等价产物，用于在无 tsc 的环境下运行测试。
