# line-diff-session

在两份长文本之间维护行差异块的 TypeScript 库：一侧局部编辑后增量重算、前后文折叠、双向行号映射；重复行有确定的决胜策略；公开偏移一律使用 UTF-16 且不会切开代理对；LF 与 CRLF（含混用、孤立 CR）全覆盖。零依赖，仅用 Node.js 内置 `node:test` 验证。

## 快速开始

需要 Node.js ≥ 23.6（原生执行 TypeScript，无需编译）。

```bash
npm test          # node --test test/*.test.ts
npm run demo      # node examples/demo.ts
```

## API 概览

`src/index.ts` 导出的核心类是 `LineDiffSession`：

```ts
import { LineDiffSession } from './src/index.ts';

const s = new LineDiffSession(leftText, rightText);

s.getBlocks();                         // 差异块（插入/删除/替换），半开区间
s.applyEdit('right', start, end, txt); // 用 UTF-16 偏移在一侧做局部编辑
s.setText('left', txt);                // 整体替换一侧
s.mapLine('left', 7);                  // 双向行号映射；无对应行返回 null
s.getView(3);                          // 前后文折叠视图：change/equal/fold
s.lineColToOffset('left', 5, 16);      // (行, 列) -> UTF-16 偏移
s.offsetToLineCol('right', 54);        // UTF-16 偏移 -> (行, 列)
```

`DiffBlock` 为 `{ kind: 'insert' | 'delete' | 'replace', left: {start,end}, right: {start,end} }`，行区间均为 0 基半开区间。

## 语义约定

- **差异计算**：Myers O(ND) 贪心算法 + 公共前后缀裁剪（2 万行文件中的局部小编辑重算约 20ms）。平局时按固定规则选择（`V[k-1] < V[k+1]` 时走插入分支），对相同输入永远产出相同结果。搜索轨迹按行紧凑存储，内存为 O(D²)；当编辑距离 D 超过 4096（几乎全文不同的病态输入）时退化为单个 replace 块，保证时间与内存有界。
- **重复行决胜**：差异块生成后统一向文件末尾方向滑动（git-xdiff 风格）。多个重复空行时，删除/插入会落到最低（最靠后）的可能位置——例如三个空行变成两个，删除落在紧邻锚点行的最后一个空行，而不是第一个。
- **双向行号映射**：相同区域的行一一对应（按前面各块的长度差平移）；插入、删除、替换块内的行在另一侧没有对应行，`mapLine` 返回 `null`——删除行映射为空，绝不错位映射到邻行。
- **折叠视图**：`getView(contextLines)` 返回 `change` / `equal`（可见上下文）/ `fold`（折叠的相同行，含两侧行范围与行数）；折叠只是视图，映射在折叠区域上始终有效。改动还原后差异块消失，视图退化为单个 `fold`。
- **局部编辑**：`applyEdit` 按 UTF-16 偏移替换一侧文本并重算；偏移会被夹紧、交换并向外侧扩张，保证代理对完整，返回实际替换区间。撤销编辑即恢复原差异状态。
- **UTF-16 偏移**：列与偏移均为 UTF-16 码元单位（JS 字符串下标）。落在代理对中间时，编辑边界向外侧扩张、定位接口向回吸附。
- **行尾**：每行独立保留自己的终止符（`LF`/`CRLF`/`CR`/`NONE`，可混用），`joinLines(splitLines(text)) === text`。行比较只看内容，不看行尾风格。

## 运行样例

`npm run demo` 的实际输出（左侧 CRLF、右侧 LF，含 emoji 与多个重复空行）：

```text
=== diff blocks (left CRLF vs right LF) ===
delete  left[3,4)  right[3,3)
replace left[6,7)  right[5,7)
delete  left[9,10)  right[9,9)

=== folded view (context = 1) ===
  ⋮ 2 equal lines folded (left 0..1)
  L2 = R2  ""
- L3 -> null  ""
  L4 = R3  "## Highlights"
  L5 = R4  "- Fast renderer 🚀"
- L6 -> null  "- Stable API"
+ R5 -> null  "- Stable API v2"
+ R6 -> null  "- New diff engine"
  L7 = R7  ""
  L8 = R8  "## Fixes"
- L9 -> null  "- Fix crash on exit"
  L10 = R9  "- Improve logging"

=== bidirectional line mapping ===
mapLine(left, 5) = 4
mapLine(left, 6) = null
mapLine(right, 6) = null
mapLine(right, 8) = 8

=== UTF-16 offsets (surrogate safe) ===
'🚀' starts at col 16 (UTF-16 units), length 2
lineColToOffset(left, 5, 17) snaps to pair start: 54
offsetToLineCol of that offset: { line: 5, col: 16 }

=== local edit on the right side, then revert ===
after insert, blocks:
  insert left[0,0) right[0,1)
  delete left[3,4) right[4,4)
  replace left[6,7) right[6,8)
  delete left[9,10) right[10,10)
mapLine(left, 0) = 1
after revert, blocks: [
  {
    kind: 'delete',
    left: { start: 3, end: 4 },
    right: { start: 3, end: 3 }
  },
  {
    kind: 'replace',
    left: { start: 6, end: 7 },
    right: { start: 5, end: 7 }
  },
  {
    kind: 'delete',
    left: { start: 9, end: 10 },
    right: { start: 9, end: 9 }
  }
]
view items: fold, equal, change, equal, change, equal, change, equal
mapLine(left, 10) = 9
```

## 测试结果

`npm test`（Node.js 内置 test runner，16 个用例）：

```text
✔ identical texts produce no blocks (0.986708ms)
✔ insertion: surrounding lines map shifted, inserted lines map to null (0.291375ms)
✔ deletion: deleted lines map to null, never to a neighbour (0.164791ms)
✔ replace: both sides of the block map to null (0.195ms)
✔ repeated lines: deterministic tie-break attributes change to the lowest position (0.234834ms)
✔ repeated empty lines: stable diff, deletion slid down next to the anchor (0.082375ms)
✔ many repeated empty lines: stable across runs, only empty lines deleted (0.354791ms)
✔ line splitting round-trips LF, CRLF, lone CR, mixed and missing final EOL (0.169833ms)
✔ LF and CRLF compare by content; terminator style is preserved per side (0.289375ms)
✔ UTF-16: surrogate pairs are never split by offsets or edits (0.418125ms)
✔ UTF-16: offsets inside CRLF terminators resolve to line content end (0.132666ms)
✔ local edit updates blocks; reverting removes them and folding still maps (0.203042ms)
✔ folded view collapses context around a change in a long text (0.124334ms)
✔ empty texts and one-sided content (0.219541ms)
✔ line/offset round-trip across every line of a mixed-EOL document (0.176ms)
✔ long text: distant small edits stay separate small blocks; revert restores (41.60525ms)
ℹ tests 16
ℹ suites 0
ℹ pass 16
ℹ fail 0
ℹ cancelled 0
ℹ skipped 0
ℹ todo 0
ℹ duration_ms 223.735
```

测试覆盖：相同文本无差异；插入/删除/替换三类块；删除行映射 `null` 而非邻行；重复行与多处重复空行的稳定决胜；连续两次 diff 结果一致；LF/CRLF/CR/混用/无尾换行的切分与还原；CRLF 文档的偏移定位；emoji 代理对在偏移与编辑中不被切开；局部编辑后更新、撤销后差异块消失且折叠映射有效；长文档折叠结构；空文档边界。
