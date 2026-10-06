# log-extractor-regex

面向日志提取场景的正则匹配库（TypeScript 实现，零依赖）。

## 语法

| 结构 | 示例 | 含义 |
| --- | --- | --- |
| 字面字符 | `a` `=` ` ` | 匹配该字符本身 |
| 转义 | `\*` `\|` `\(` `\)` `\\` | 元字符的字面化（`\` 后任意字符均按字面处理） |
| 连接 | `abc` | 顺序匹配 |
| 或 | `a\|bc` | 任一分支 |
| 星号 | `a*` `(ab)*` | 零次或多次重复 |
| 捕获括号 | `(a+式)` → `(a*)` | 捕获组，按左括号顺序从 1 编号 |

空模式、空分支（`a|`）、空组（`()`）均合法，匹配空串。

## 匹配语义

对输入串（长度 ≤ 20）按以下优先级确定唯一结果：

1. **最左起点**：取存在匹配的最小起始下标。
2. **最长整体匹配**：在该起点下取结束下标最大者。
3. **按捕获组顺序选最长**：整体长度并列时，依次比较组 1、组 2、…，
   每组按"参与优先于未参与，参与时跨度更长者优先"排序。
4. **未参与 ≠ 空串**：未参与的组结果为 `undefined`；参与了但匹配空串的组
   结果为 `''`，两者严格不同（如 `(a*)` 在 `"b"` 上组 1 为 `''`，而
   `(a)|(b)` 在 `"b"` 上组 1 为 `undefined`）。

星号迭代中，**匹配空串的迭代必须是最后一次迭代**——这既终止了
`(a*)*`、`()*` 这类空匹配循环，又保留了"空迭代可让捕获组参与"的语义
（`(a*)*` 在 `''` 上组 1 为 `''`）。

## API

```ts
import { searchPattern } from './src/index.js';

const m = searchPattern('level=(a|b)*', 'x level=abba y');
// m = { start: 2, end: 12, match: 'level=abba', groups: ['a'] }
```

- `searchPattern(pattern, input): SearchMatch | null` — 解析并搜索。
- `searchAst(ast, groupCount, input): SearchMatch | null` — 复用已解析模式。
- `parsePattern(source): { ast, groupCount }` — 解析为正则语法树。
- `enumerateTrees(ast, input, pos): Generator<Tree>` — 枚举全部解析树（惰性）。
- `treeCaptures(tree, groupCount)` — 由解析树提取各组跨度。
- `enumMatchVectors(ast, input, pos, groupCount)` — 去重/剪枝后的匹配向量。
- `compareMatchVectors(a, b)` — 匹配向量排序（整体最长，再按组序最长）。

`SearchMatch.groups[i]` 为第 `i+1` 组的捕获文本，`undefined` 表示未参与。

## 实现要点

- **解析树枚举**（`src/enumerate.ts`）：生成器递归枚举字面、连接、或、
  星号、捕获组的全部解析树；星号通过"空迭代必为末次"规则保证终止。
  这是语义的权威参考实现。
- **匹配向量 + 支配剪枝**（`src/vectors.ts`）：主匹配路径。每个子表达式
  在每个结束位置只保留捕获向量字典序最优者——由于后续子表达式只会覆盖
  自身子树内的组，被支配的向量不可能在全局反超，剪枝安全。星号按结束
  位置递增做动态规划（非空迭代严格推进位置，空迭代仅作终态记录），
  因此 `((((a*)*)*)*)*` 在 20 字符输入上也在毫秒级终止。
- **结果选择**（`src/search.ts`）：起点递增扫描，命中即按
  `compareMatchVectors` 取最优。

## 验证

`npm test`（Node ≥ 16，无需安装依赖）执行 `test/run.mjs`：

1. 解析器单元测试（组编号、非法模式报错、空分支/空组）。
2. 语义表驱动测试（最左、最长、组序最长、`undefined` 与 `''` 区分等）。
3. **解析树交叉验证**：对精选模式与 150 个随机模式 × 数十个输入，
   用 `enumerateTrees` 穷举全部解析树、以独立实现的比较器折叠出最优解，
   与主匹配路径逐一比对；同时用独立的 `possibleEnds` 递归匹配器校验
   两个枚举器的可达结束位置集合完全一致。
4. 终止性测试：`(a*)*`、`((a*)*)*`、`(a|a|a|a|a)*` 等在 20 字符输入上
   限时完成。

说明：环境无 TypeScript 工具链时，`test/run.mjs` 会先用
`scripts/strip-types.mjs`（仅支持 `src/` 所用受控子集的类型擦除器）把
`src/*.ts` 转为 `.build/*.js` 再测试；有 `tsc` 时可运行 `npm run check`
做完整类型检查。
