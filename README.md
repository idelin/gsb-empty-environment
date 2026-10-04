# chunked-sentence-segmenter

增量（分块）断句库。规则由三部分组成：**缩写词典**（可按版本切换）、
**引号配对**、**终止标点**。结尾尚未确定时保留未决区（pending），
不会把缩写句点提前提交；已提交句子的来源区间（全局偏移）永久稳定。

## 运行要求

Node.js ≥ 22.18 或 ≥ 23.6（直接运行 TypeScript，无需编译）。

```sh
npm test
```

## 用法

```ts
import { SentenceSegmenter, segment } from "./src/index.ts";

const seg = new SentenceSegmenter({
  dictionaries: {
    v1: ["Dr", "Mr", "U.S", "e.g"],
    v2: ["Mr"],
  },
  dictionaryVersion: "v1",
});

seg.push("I met Dr.");        // => []      缩写句点不提交
seg.push(" Smith.");          // => []      结尾未确定，保留未决区
seg.push(" He left.");        // => ["I met Dr. Smith."]，"He left." 进入未决区
seg.setDictionaryVersion("v2"); // 只影响未决区及之后的输入
seg.flush();                  // => ["He left."]

// 全量（批式）状态机，用于对照：
segment("One. Two.", { dictionaries: { v1: [] } });
```

每个提交的句子带有 `start` / `end`（全局流偏移，左闭右开），
`text === 原始输入.slice(start, end)` 恒成立。

## 规则

1. `.` 在以下情况**不是**终止符：
   - 后随 `.`（省略号）、字母或数字（`U.S`、`2.5`、`Mr.Dr`）；
   - 句点前 `[A-Za-z.]` 的最长串命中当前版本缩写词典
     （取最长 token，前缀重叠项如 `Mr` / `Mr.Dr` 以完整 token 为准）。
2. 终止符（默认 `.!?。！？…`）结束句子；紧随的连续终止符（`?!`）与
   匹配引号栈的闭引号按任意顺序吸收进当前句子（`"yes".` 视为一句）。
3. 边界之后必须还存在后续字符才提交（或 `flush` 时）；否则整段尾部
   留在未决区，可通过 `pending` / `pendingStart` 查看。

## 测试

`test/segmenter.test.ts` 覆盖：连续引号、词典前缀重叠项、空段、
未决区行为、词典版本切换后已提交区间的稳定性，以及逐字符 / 定长块 /
任意切块输入与全量状态机 `segment()` 的结果对照。
