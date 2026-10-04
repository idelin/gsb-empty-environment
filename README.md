# edit-distance-paths

编辑距离库：接收非负操作成本，返回最小编辑成本与最优操作路径数（`bigint` 计数），并支持确定性的"第 K 条最优路径"反排名（unranking）。

## 安装与运行

源码为 TypeScript（仅使用可擦除语法），Node.js ≥ 22.18 可直接运行，无需编译：

```sh
node --test        # 运行测试
```

如需类型检查或编译，安装 `typescript` 与 `@types/node` 后执行 `npx tsc --noEmit`（见 `tsconfig.json`，已开启 `strict` 与 `erasableSyntaxOnly`）。

## API

```ts
import {
  analyzeEditDistance,   // 完整分析：cost + count + unrank
  editDistance,          // 便捷封装：仅 { cost, count }
  kthOptimalPath,        // 一次性取第 K 条最优路径
  DEFAULT_COSTS,         // { insert: 1, delete: 1, substitute: 1 }
  DEFAULT_MOVE_ORDER,    // ['diagonal', 'delete', 'insert']
} from 'edit-distance-paths';

const analysis = analyzeEditDistance('kitten', 'sitting', { insert: 1, delete: 1, substitute: 1 });
analysis.cost;   // 3（number）
analysis.count;  // 最优路径数（bigint）
analysis.unrank(0);      // 第 0 条最优路径：EditOperation[]
analysis.unrank(analysis.count - 1n);  // 最后一条
```

### 成本规则

- 三种操作成本必须是有限的 `number`：`insert`、`delete`、`substitute`。
- **禁止零消耗操作**：`insert` 与 `delete` 必须 `> 0`，否则抛 `RangeError`（零成本插入/删除会产生退化的对齐填充）。
- `substitute` 允许为 `0`（零成本替换），这会合法地产生多条最优路径。
- 相同字符的 `match` 恒为 0 成本，不可配置。
- 负成本、`NaN`、`Infinity` 一律抛错；非 `number` 类型抛 `TypeError`。
- 成本以 float64 累加并做严格相等比较，建议使用整数成本；非整数成本受浮点求和精度影响。

### 路径计数语义

- 路径 = 操作对齐序列（`match` / `substitute` / `delete` / `insert`）。
- **区分不同路径而非最终字符串**：不同操作序列即使产生相同的最终字符串（例如重复符号 `aa` → `a` 删除任一个 `a`）也分别计数。
- 计数使用 `bigint`，可精确表示超过 2^53 的路径数（例如 60 个 `a` → 30 个 `a` 的计数为 `118264581564861424n`）。
- 空路径（`''` → `''`）计为 1 条。

### 第 K 条最优路径（确定性反排名）

- `unrank(k)` 中 `k` 为 **0 起始** 索引，合法范围 `0 <= k < count`，越界抛 `RangeError`。
- `k` 接受 `number`（必须为安全整数）或 `bigint`（计数超过 2^53 时必须用 `bigint`）。
- 相同输入恒返回相同路径；`0..count-1` 遍历产生互不相同的全部最优路径。

### 边界顺序（公开约定）

每个 DP 单元格按固定的移动顺序枚举最优前驱，默认顺序公开为常量：

```ts
DEFAULT_MOVE_ORDER = ['diagonal', 'delete', 'insert']
```

- `diagonal`：`match`（相同字符，0 成本）或 `substitute`；
- `delete`：消耗一个源字符；`insert`：产生一个目标字符。
- 边界单元格（首行/首列）只有一种合法移动，顺序唯一确定。
- 第 K 条路径即按此顺序对全部最优路径做字典式编号的结果；可通过 `options.moveOrder` 传入三种移动的任意排列来自定义编号顺序（不改变 `cost` / `count`，只改变 K 与路径的对应关系）。

### EditOperation

```ts
interface EditOperation {
  kind: 'match' | 'substitute' | 'delete' | 'insert';
  sourceIndex: number;          // 源串中的位置（insert 为插入点）
  targetIndex: number;          // 目标串中的位置（delete 为当前目标位置）
  sourceChar: string | undefined;
  targetChar: string | undefined;
  cost: number;                 // 该操作的成本
}
```

操作按从源到目标的正向顺序返回，依次回放可将 `source` 变换为 `target`。字符串按 Unicode 码位（`Array.from`）索引。

## 测试

`test/edit-distance.test.ts` 使用 Node 内置 test runner：

- 成本校验（禁止零消耗插入/删除、允许零成本替换、负值/NaN/Infinity 抛错）；
- 重复符号与零成本替换造成的多路径用例（区分路径与相同最终字符串）；
- 小序列穷举动态枚举核对：字母表 `{a,b}` 长度 ≤ 4、`{a,b,c}` 长度 ≤ 3 的全部串对 × 多组成本（含 `substitute: 0`），逐条比对暴力枚举出的最优路径集合与顺序；
- `bigint` 大计数（C(60,30)）与越界/类型错误；
- 边界顺序公开性与自定义 `moveOrder` 的一致性。
