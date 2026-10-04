# edit-distance-paths

编辑距离库：接收正数操作成本，返回最小成本与最优操作路径数（`bigint` 精确计数），
并支持第 K 条最优路径的确定性反排名（unrank）。

## 语义约定

- **操作**：插入、删除、替换。相同字符的"替换"（match）是免费的空操作，不属于可配置操作。
- **成本**：`insert` / `delete` / `substitute` 必须是大于 0 的有限数。
  零成本与负成本操作被禁止，`validateCosts` 会抛出 `RangeError`（非有限数抛 `TypeError`）。
- **计数**：`count` 统计的是不同的**操作路径**（编辑脚本）数量，而不是不同结果字符串的数量。
  两条路径只要有一步不同就算两条，即使可观察效果完全相同
  （例如从 `"aa"` 删除第一个或第二个 `'a'`）。计数使用 `bigint`，不经过浮点。
- **字符**：按 Unicode 码位（code point）处理，emoji 等代理对字符视为单个字符。

## 排名顺序（公开的边界顺序）

最优路径按操作序列从左到右字典序排列，移动种类的优先级由导出的常量
`MOVE_ORDER = ['diagonal', 'delete', 'insert']` 给出（`diagonal` 涵盖 match 与 substitute）。

- `unrank(0)` 返回字典序最小的最优路径，`unrank(count - 1n)` 返回最大的。
- 顺序是全序且确定的：相同输入永远得到相同的第 K 条路径。
- `k` 越界（`k < 0` 或 `k >= count`）抛 `RangeError`。

## API

```ts
import {
  EditDistance,        // 预计算类：.cost / .count / .unrank(k)
  editDistance,        // 便捷函数 -> { cost: number, count: bigint }
  unrankPath,          // 便捷函数 -> Operation[]
  applyPath,           // 将路径应用到源串，返回结果串（不一致时抛错）
  pathCost,            // 路径在给定成本下的总成本
  validateCosts,       // 成本校验
  MOVE_ORDER,          // 公开的边界顺序常量
  enumerateOptimalPaths, // 暴力枚举（指数级，仅供小序列核对）
} from 'edit-distance-paths';

const ed = new EditDistance('aa', 'a');
ed.cost;        // 1
ed.count;       // 2n —— 删第一个 'a' 与删第二个 'a' 是两条不同路径
ed.unrank(0n);  // [{ type: 'match', char: 'a' }, { type: 'delete', char: 'a' }]
ed.unrank(1n);  // [{ type: 'delete', char: 'a' }, { type: 'match', char: 'a' }]
```

## 运行测试

需要 Node.js >= 23.6（内置 TypeScript 类型擦除，无需编译）：

```sh
npm test
```

测试包含小序列动态枚举核对：对字母表 `{a, b}` 上长度 ≤ 4 的全部串对、
多组成本，逐条比较 DP 计数与暴力枚举的最优路径列表（含零成本 match
与重复符号造成的多路径情形），并验证每个 `unrank(k)` 与枚举顺序完全一致。

## 复杂度

- 时间与空间：`O(m·n)`；计数为大整数，位长随路径数对数增长。
- `unrank` 首次调用时构建后缀表（`O(m·n)`），之后每条路径 `O(m+n)`。
- 成本为整数时所有比较精确；小数成本受浮点精度限制，建议使用整数成本。
