# ts-table-paginator

按行高分页的 TypeScript 表格排版库：表头每页重复占高，合并单元格跨行形成的
行组不可拆分，尾注仅在最后一页占高；行组加表头超页时报出具体组并立即终止，
绝不无限新建页。

零运行时依赖，直接使用 Node ≥ 22.6 的原生类型擦除运行 `.ts`（或任意 TS 工具链）。

## 用法

```ts
import { groupsFromRows, paginate } from 'ts-table-paginator';

const rows = [
  { id: 'r0', height: 30 },
  { id: 'r1', height: 20 },
  { id: 'r2', height: 20 },
  { id: 'r3', height: 30 },
];

// 合并单元格跨第 1~2 行 → 这两行聚成不可拆行组
const groups = groupsFromRows(rows, [{ start: 1, end: 2 }]);

const pages = paginate(groups, {
  pageHeight: 60,     // 整页可用高度
  headerHeight: 10,   // 表头，每页重复
  footnoteHeight: 8,  // 尾注，仅最后一页
});
// pages[i] = { index, groups, contentHeight, headerHeight, footnoteHeight, usedHeight, isLast }
```

也可以绕过 `groupsFromRows`，直接构造 `RowGroup[]` 传给 `paginate`。

## 排版规则

- 每页内容区容量 = `pageHeight - headerHeight`，表头在每页重复占用；
- 行组是不可拆的最小单元：要么整组落页，要么整组换页；
- 末页容量 = 内容区容量 − `footnoteHeight`，尾注只在最后一页占高；
- **末页回流**：贪心装满普通页后若末页只剩表头尾注（空白内容页），
  会把最后一组回流到末页；只有当最后一组本身放不进末页容量时，
  才保留空白末页承载尾注；
- 空文档返回一页（仅表头 + 尾注）。

## 超页报错（不无限新建页）

分页前先校验每个行组：组高 > 内容区容量时抛出 `PaginationOverflowError`，
错误携带 `kind: 'group'`、`groupId`、`groupIndex`、`requiredHeight`、
`availableHeight`，指明具体是哪一组放不下。表头占满整页
（`kind: 'header'`）与尾注超过末页内容区（`kind: 'footnote'`）同样立即报错。

算法上界为 n+1 页（n 为组数）：每组单独都能放进普通页，因此枚举页数
k = 1..n+1 必然终止，从结构上杜绝无限新建页。

## 算法

1. 校验容量与每个行组（失败即抛 `PaginationOverflowError`）；
2. 预计算 `packed[p]`：p 个满容量普通页按贪心最多能装多少组
   （普通页容量相同，贪心前缀装载最优）；
3. 枚举总页数 k，取第一个满足“剩余组高度 ≤ 末页容量”的 k；
4. 应用末页回流修正后按贪心边界切分出页。

时间复杂度 O(n²) 最坏（实际 O(n) 次扫描 + O(n) 次高度求和）。

## 测试

```sh
npm test        # node --test，原生类型擦除，无需编译
npm run typecheck  # 需要本机装有 typescript
```

- `test/paginate.test.ts`：单元测试（表头重复、行组原子性、具体组报错、
  尾注仅末页、回流与空白末页等边界）；
- `test/enumeration.test.ts`：**小规模划分枚举核对**——对 n = 1..7 行枚举
  全部 2^(n−1) 种行组划分 × 多组页面配置，逐项断言布局不变量（顺序保持、
  组不跨页、每页表头、尾注仅末页、页数 ≤ n+1），并用独立的暴力划分
  oracle 核对页数最优性；另有有/无尾注的交叉对比，验证“尾注放得下则不
  扰动布局，放不下则必然回流”。
