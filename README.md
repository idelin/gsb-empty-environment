# tree-grid-nav

树表（tree-table）键盘导航库。所有导航都运行在**扁平可见网格模型**之上：
可见行 = 祖先全部展开的行（DFS 序），可见列 = 当前存活的列。隐藏行对导航
完全不可达。

## 模型

```ts
import { TreeGrid } from './index.ts';

const grid = new TreeGrid({
  columns: [{ key: 'name' }, { key: 'size' }],
  nodes: [
    { id: 'src', expanded: true, children: [{ id: 'src/a.ts' }, { id: 'src/b.ts' }] },
    { id: 'dist', expanded: false, hasChildren: true }, // 懒加载
  ],
  isCellDisabled: (node, column) => node.id === 'dist' && column.key === 'size',
});

grid.getVisibleGrid(); // { rows: FlatRow[], columns: ColumnDef[] } —— 核对用扁平投影
```

## 导航

- `moveUp / moveDown / moveLeft / moveRight`：在可见单元间移动，**跳过禁用单元**
  （行禁用、列禁用、`isCellDisabled` 谓词三者任一）。到边界则保持不动。
- `focusCell(rowId, colKey)`：程序化聚焦；隐藏行、未知列、禁用单元一律拒绝，
  **不能跳进隐藏行**。
- `getFocus()`：返回 `{ rowId, colKey } | null`，读取前自动校正焦点。

## 折叠回退

`collapse(id)` 隐藏子树后，若焦点在被隐藏的行上：

1. 焦点回到**最近可见祖先行**（跳过同样被隐藏的中间祖先）；
2. 列保持不变；若该列在祖先行上不可用（被删除或禁用），取**最近左列**
   （同样跳过禁用单元）；左侧全部不可用时才回退到最近右列；都不行则清空焦点。

## 异步展开

```ts
await grid.expandAsync('dist', async (node) => fetchChildren(node.id));
```

加载完成后子行挂入树、行展开，扁平网格与焦点同步校正。同一行的并发
`expandAsync` 共享一次加载（`isLoading(id)` 可查状态）。

## 列删除

`removeColumn(key)` 后，焦点若在被删列上，按上面的"最近左列"规则重新定位；
`addColumn(col, index?)` 保持焦点不动。

## 测试

需要 Node ≥ 24（原生 TypeScript 类型擦除 + 内置 test runner）：

```sh
npm test
```

测试通过 `getVisibleGrid()` 的扁平投影核对异步展开与列删除后的行/列集合及焦点位置。
