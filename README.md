# three-way-merge

基于共同祖先（base）的行级三路文本合并库，TypeScript 实现，零依赖。

- 互不重叠的修改**自动合并**（如一侧改首行、另一侧改末行）。
- 重叠修改生成**结构化冲突**，冲突块同时完整保留两侧内容，绝不静默丢弃。
- 两侧**相同的修改只保留一份**，不产生冲突。
- 输出**保留 base 的换行风格**（LF / CRLF 均已覆盖），可用 `eol` 选项强制指定。
- 对外暴露的文本偏移一律为 **UTF-16 码元偏移**（即 JS 字符串下标），且全部落在行边界上，**不会切开代理对**（emoji 等增补平面字符安全）。

## 环境要求

Node.js ≥ 23.6（原生类型剥离，直接运行 `.ts`，无需编译）。如装有 TypeScript，也可 `npm run typecheck` 做类型检查。

## 使用

```ts
import { merge3 } from "./src/merge.ts";

const result = merge3(base, ours, theirs, {
  oursLabel: "ours",     // 冲突标记中本侧标签
  theirsLabel: "theirs", // 冲突标记中对侧标签
  eol: "auto",           // "auto" | "lf" | "crlf"
});

result.text;       // 合并后文本（冲突处以标记块呈现）
result.clean;      // 是否无冲突
result.eol;        // 实际使用的换行风格 "\n" | "\r\n"
result.conflicts;  // 结构化冲突数组：
// {
//   startOffset, endOffset,  // 冲突块在 text 中的 UTF-16 偏移（含标记行）
//   base: string[],          // 冲突区域的 base 行
//   ours: string[],          // 本侧替换内容
//   theirs: string[],        // 对侧替换内容
// }
```

## 合并规则

1. 对 base 与两侧分别做行级 diff（LCS），得到各自的修改块。
2. base 行区间**相交**的两侧修改、或**同一位置插入不同内容**，划入同一区域：
   - 仅一侧修改 → 采用该侧；
   - 两侧修改完全相同 → 采用一份；
   - 否则 → 生成结构化冲突（`<<<<<<< / ======= / >>>>>>>` 标记块，两侧内容都保留）。
3. 相邻但不相交的修改（如一侧改第 2 行、另一侧改第 3 行）各自独立，自动合并。
4. 文件末尾换行符的有无也参与比较，不会因合并丢失。

## 运行

```bash
npm test    # Node.js 内置测试运行器（node:test），21 个用例
npm run demo  # 运行样例
```

## 运行样例（npm run demo）

```
=== 场景 1：首行/末行互不重叠修改（LF）===
clean = true, conflicts = 0
--- 合并结果 ---
APPLE
banana
CHERRY

=== 场景 2：同一行被双方改成不同值（冲突）===
clean = false
--- 合并结果 ---
title: demo
<<<<<<< ours
version: 2
=======
version: 3
>>>>>>> theirs
author: team

结构化冲突： {
  startOffset: 12,
  endOffset: 70,
  base: [ 'version: 1' ],
  ours: [ 'version: 2' ],
  theirs: [ 'version: 3' ]
}

=== 场景 3：CRLF + 同位置插入不同内容（冲突）===
clean = false, eol = "\r\n"
合并结果（转义显示）：
"header\r\n<<<<<<< ours\r\nadded-by-ours\r\n=======\r\nadded-by-theirs\r\n>>>>>>> theirs\r\nfooter\r\n"

=== 场景 4：emoji 文本的 UTF-16 冲突偏移 ===
startOffset = 11, endOffset = 59
冲突块文本： "<<<<<<< ours\nOURS\n=======\nTHEIRS\n>>>>>>> theirs\n"
```

## 测试结果（npm test）

```
✔ 互不重叠修改自动合并：一侧改首行，另一侧改末行（LF）
✔ 互不重叠修改自动合并：一侧改首行，另一侧改末行（CRLF）
✔ CRLF 风格在自动合并与冲突标记中都得到保留
✔ LF 文件不会被引入 CRLF
✔ 重叠修改生成结构化冲突，且两侧内容都保留
✔ 双方同位置插入不同内容 → 按规则冲突
✔ 相同修改只出现一次，且不算冲突
✔ 两侧在相同位置插入相同内容 → 只保留一份
✔ 一侧删除、另一侧修改不相交区域 → 自动合并
✔ 只有一侧有修改时直接采用该侧
✔ 空 base：双方都新增不同内容 → 冲突且内容都在
✔ 空 base：仅一侧新增 → 干净合并
✔ 末尾换行差异不丢失：一侧补末尾换行，另一侧改首行
✔ UTF-16 偏移：含 emoji（代理对）时冲突偏移精确且不切开代理对
✔ UTF-16 偏移：CRLF 下同样精确
✔ isUtf16Boundary 能识别代理对中间的位置
✔ detectEol：CRLF 占优判 CRLF，否则 LF
✔ options.eol 可强制输出换行风格
✔ 自定义冲突标签
✔ 多区域：两个冲突 + 一个自动合并区域
✔ 一侧在文件开头插入、另一侧在文件末尾插入 → 自动合并

ℹ tests 21
ℹ pass 21
ℹ fail 0
```

## 实现说明

- `src/merge.ts`：`merge3` 入口、LCS 行级 diff、区域归并、冲突渲染与 UTF-16 偏移回填。
- 内部把行统一规范化为 `\n` 结尾进行比较，输出时转换回 base 的换行风格。
- diff 采用公共前后缀裁剪 + LCS 动态规划，复杂度 O(N·M)（N、M 为去掉公共前后缀后的行数），适合源代码规模的文本。
- 冲突块内每行强制带行尾（标记行需独占一行）；自动合并区域则精确保留原有的末尾换行状态。
