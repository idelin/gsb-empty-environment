# fix-combiner — 自动修复组合工具

单独适用的修复建议合在一起未必适用。本工具在**重叠**与**前置关系**约束下，
求**总收益最大的兼容修复集合**（默认最多 25 项），应用过程中**重新检查前置条件**，
并为每条未采纳的建议输出**冲突证据**。

## 使用

```bash
npm install        # 安装 typescript（devDependency）
npm test           # 编译并运行测试
npm run demo       # 运行端到端演示
```

```ts
import { combineFixes, textContains } from 'fix-combiner';

const result = combineFixes(
  { 'src/a.ts': '...文件内容...' },
  [
    {
      id: 'fix-1',
      description: '把 total 改名为 sum',
      benefit: 8,
      edits: [{ file: 'src/a.ts', start: 35, end: 51, replacement: '  let sum = 0;' }],
      writesSymbols: ['total'],
    },
    {
      id: 'fix-2',
      description: '同步修改 return 语句',
      benefit: 6,
      requires: ['fix-1'],                              // 前置关系
      edits: [{ file: 'src/a.ts', start: 107, end: 122, replacement: '  return sum;' }],
      preconditions: [textContains('src/a.ts', 'return total')], // 应用后会被重新检查
    },
  ],
  { maxCount: 25 },
);
// result.selected / result.totalBenefit / result.rejected（含冲突证据）/ result.finalFiles
```

## 兼容性判定（不仅看文本范围）

`src/conflicts.ts` 同时检测五类冲突：

| 类型 | 说明 |
| --- | --- |
| `text-overlap` | 同文件编辑区间相交，或同一位置的双重插入 |
| `symbol-write-write` | 两条建议改写同一符号（文本不重叠也冲突） |
| `symbol-write-read` | 一条改写另一条依赖的符号，且二者间无 `requires` 排序 |
| `explicit-invalidation` | 建议通过 `invalidates` 显式声明使对方失效 |
| `mutex-group` | 同一互斥组最多选中一个 |

## 求解流程（`src/combine.ts`）

1. **初始前置检查**：`preconditions` 先对原始文件求值，不满足直接拒绝；
2. **冲突检测**：生成两两冲突证据；
3. **最大收益选择**（`src/selector.ts`）：最大权独立集 + 前置闭包约束
   （选中 v 必须选中 `requires` 传递闭包），分支限界精确求解，数量 ≤ 25；
   前置未知/成环/闭包内部冲突的建议判为结构性不可行；
4. **应用 + 重检**（`src/applier.ts`）：按拓扑序（前置先应用）应用编辑，
   偏移经 `OffsetMap` 平移到当前内容；**每次应用后重新检查**剩余修复的前置条件，
   失效者及其依赖者被丢弃并记录证据；
5. **证据输出**：每条未选中建议给出具体原因——与已选修复的冲突明细
   （含区间与符号）、缺失/不可行的前置、前置环、应用后前置失效（含破坏者）、
   或收益不足。

## 未选中证据类型（`RejectionReason`）

- `conflict`：与最终应用集合的冲突列表（含种类与细节）
- `missing-prerequisite` / `unknown-prerequisite` / `prerequisite-cycle` / `prerequisite-conflict`
- `initial-precondition-failed`：选择前前置条件即不满足
- `post-apply-precondition-failed`：应用其他修复后前置条件被破坏（附破坏者列表）
- `dependent-dropped`：因前置被丢弃而连带丢弃
- `benefit-too-low`：无冲突但在 25 项预算下纳入它不能提高总收益
