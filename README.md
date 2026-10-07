# longest-non-overlapping-repeat

TypeScript 文本分析库：在文本中查找**至少出现 k 次、且这些出现区间可两两不重叠**的最长子串。

## 语义

- 子串 `t` 合格当且仅当它在文本中的出现位置里，能选出至少 `k` 个**两两不重叠**的区间。
  注意区分两个概念：
  - `occurrenceCount`：全部出现次数（含重叠），如 `"aa"` 在 `"aaaa"` 中出现 3 次；
  - `maxNonOverlapping`：最多能选出的不重叠出现次数，上例中只有 2 次。
  因此 `"aaaa"` 在 `k = 3` 时答案是 `"a"` 而不是 `"aa"`。
- 决胜规则：先取最长；并列时按子串**字节序**（UTF-16 码元序，即 JS 字符串原生 `<`；
  纯 ASCII 下等同 UTF-8 字节序）；再并列按选中起点列表的字典序。
- 空输入（空串）或无任何合格子串时返回 `null`；`k` 必须是正整数，否则抛 `RangeError`。

## API

```ts
import {
  longestNonOverlappingRepeat,
  findOccurrences,
  selectNonOverlapping,
  compareByteOrder,
} from "longest-non-overlapping-repeat";

const result = longestNonOverlappingRepeat("catdogcatdog", 2);
// { substring: "catdog", starts: [0, 6], occurrenceCount: 2, maxNonOverlapping: 2 }
```

- `longestNonOverlappingRepeat(text, k)` → `NonOverlappingRepeat | null`
- `findOccurrences(text, pattern)` → 全部出现起点（含重叠）
- `selectNonOverlapping(starts, length)` → 等长区间的贪心最早起点最大不重叠选择
- `compareByteOrder(a, b)` → UTF-16 码元序比较

## 复杂度

可行性关于子串长度单调（合格子串的前缀也合格），故对长度二分；
每个长度按子串值分组统计出现起点并贪心选择。时间 `O(n^2 log n)`，空间 `O(n^2)`。

## 测试

测试用 [Bun](https://bun.sh) 运行（无需安装依赖）：

```sh
bun test
```

`test/index.test.ts` 包含：

- 定向用例：空输入、非法 `k`、出现次数 vs 可选不重叠次数的区分、字节序决胜等；
- 贪心区间选择 vs 子集穷举选择的全量核对（所有长度 ≤ 9 的二进制文本的每个子串）；
- 库实现 vs 独立穷举参考实现（`test/brute.ts`，枚举所有子串 + 枚举所有区间子集）的交叉核对：
  所有长度 ≤ 8 的二进制文本（k = 1..4）、所有长度 ≤ 6 的三进制文本（k = 1..3）、
  以及含星外字符（代理对）的随机文本。

如安装了 TypeScript，可用 `npm run typecheck` 做类型检查。
