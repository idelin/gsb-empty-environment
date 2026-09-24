# inline-3way-merge

基于共同祖先（base）对两个分支（ours / theirs）的行内文本编辑做三方合并。
纯 TypeScript，无第三方依赖，使用 Node.js 内置测试运行器（`node:test`）。

## 声明的合并规则

1. 两侧编辑触及 base 中互不相交的区域时自动合并（例如一侧改首行、另一侧改末行）。
2. 在偏移 `p` 处的插入，只在以下情况下与另一编辑重叠：另一编辑也是在 `p` 处插入，
   或者其替换范围严格包含 `p`。在范围边界（`p === start` 或 `p === end`）的插入
   不算重叠，自动合并。
3. 重叠编辑产生的结果文本完全相同时，只应用一次（相同修改去重）。
4. 其余重叠情况生成结构化冲突，携带 `base` / `ours` / `theirs` 三侧内容；合并文本中
   同时写入冲突标记，保证不静默丢弃任何一侧内容：

   ```
   <<<<<<< ours
   ours 行
   ||||||| base
   base 行
   =======
   theirs 行
   >>>>>>> theirs
   ```

5. 输出继承 base 的主导换行风格（LF 或 CRLF，含冲突标记行）；base 末尾无换行时输出
   也保持无末尾换行。
6. 所有公开偏移均为 **UTF-16 码元偏移**（JS 字符串原生索引）；行只在 `\n` / `\r\n`
   处切分，二者都是 BMP 字符，因此永远不会切开代理对。

## API

```ts
import { merge3 } from './merge.ts';

const r = merge3(baseText, oursText, theirsText);
// r.text      合并后的文本（有冲突时含冲突标记）
// r.clean     是否无冲突
// r.conflicts: Conflict[]  结构化冲突
//   { base, ours, theirs }          三侧行内容
//   { start, end }                  输出文本中的 UTF-16 偏移（含冲突标记块）
//   { baseStart, baseEnd }          base 文本中的 UTF-16 偏移
// r.eol       输出使用的换行风格
```

## 运行

```bash
node demo.ts                  # 运行样例
node --test merge.test.ts     # 或 npm test
```

需要 Node.js >= 23.6（原生执行 TypeScript；旧版本可加 `--experimental-strip-types`）。

## 样例输出（`node demo.ts` 摘录）

```
=== LF: first-line + last-line auto-merge ===
eol: "\n", clean: true
ALPHA
beta
GAMMA

=== same-position different inserts -> conflict ===
eol: "\n", clean: false
a
<<<<<<< ours
X
||||||| base
=======
Y
>>>>>>> theirs
b

conflict @output[2, 55) base[2, 2)

=== identical change -> applied once ===
one
TWO
three

=== CRLF + emoji: overlapping edits conflict ===
eol: "\r\n", clean: false
😀 first
<<<<<<< ours
MIDDLE-OURS
||||||| base
middle
=======
MIDDLE-THEIRS
>>>>>>> theirs
last

conflict @output[10, 99) base[10, 16)

emoji line "😀 first" is 8 UTF-16 code units;
base conflict offsets [10, 16) -> "middle"
```

## 测试结果

Node v23.9.0，`node --test merge.test.ts`：16 项全部通过，覆盖

- 首行 / 末行互不重叠修改自动合并
- 同位置插入不同内容按声明规则冲突，base/ours/theirs 均保留
- 相同修改（替换与插入）只出现一次
- LF 与 CRLF 换行风格保留（含冲突标记行）、base 无末尾换行保留
- UTF-16 偏移与代理对（😀 🎵🎶）不被切开
- 空 base 双侧插入、边界插入不冲突、删除与编辑组合（含 delete vs edit 冲突）

```
ℹ tests 16
ℹ pass 16
ℹ fail 0
```

## 实现要点

- `merge.ts`：LCS 行级 diff（`computeEdits`）→ 两侧编辑按传递重叠分簇
  （`clusterEdits`）→ 逐簇应用：仅一侧修改取该侧、双侧结果相同去重、否则生成
  结构化冲突 + 标记块 → 按 base 换行风格重新拼装。
- 偏移计算基于 JS 字符串长度（即 UTF-16 码元），并扫描真实文本得到每行起始偏移，
  混合换行也不会错位。
