# completion-range-mapper

语言补全的替换范围基于**旧文档版本**返回。本库提供一条**版本编辑链**，把旧范围
映射穿过后续编辑；若编辑触及替换范围内部则结果失效，边界插入按指定的**亲和性**
映射。应用补全是**一次可撤销事务**，并用编辑链核对旧响应与多光标范围。

零依赖，TypeScript 源码直接由 Node ≥ 23 的类型擦除能力运行。

## 核心概念

- **Range**：半开区间 `[start, end)`，0 起始的 UTF-16 偏移量。
- **Edit**：把 `[start, end)` 替换为 `text`；`start === end` 时为纯插入。
- **Affinity（亲和性）**：文本恰好插入在被跟踪位置上时，位置停留（`'before'`）
  还是随插入移动（`'after'`）。范围两端点各有亲和性，预设：
  - `RangeAffinity.Expand`：边界插入被范围**吸收**（默认，适合“边输边补”）。
  - `RangeAffinity.Collapse`：边界插入被**排除**在范围外。
- **失效规则**：编辑所替换的区间与范围的严格内部相交（即删改了范围内部内容）
  → 整个范围映射失效（`undefined`）。范围内部的纯插入不算触及，范围自然扩张。

## 版本编辑链

`VersionedDocument` 维护只增不删的编辑链：每次事务追加一个链节点，版本号单调
递增；撤销/重做本身也作为新节点追加（重放逆编辑）。因此任意旧版本的范围都能
沿链线性折叠映射到当前版本，无需特判。

```ts
import { VersionedDocument, range } from 'completion-range-mapper';

const doc = new VersionedDocument('hello world');
const v0 = doc.version;            // 0
doc.insert(0, '>> ');              // v1
doc.delete(8, 13);                 // v2
doc.mapRange(range(2, 5), v0);     // 沿链映射到当前版本
doc.undo();                        // v3：撤销也产生新版本
```

## 应用补全

```ts
import { applyCompletion } from 'completion-range-mapper';

// 语言服务器在版本 v0 上返回：把 [6, 8) 替换为 "hello"
const item = { label: 'hello', insertText: 'hello',
               replaceRange: { start: 6, end: 8 }, version: v0 };

// 期间用户可能又编辑了文档——applyCompletion 会用编辑链核对并映射
const result = applyCompletion(doc, item, /* 次要光标范围 */ [range(20, 22)]);
// result: { ok: true, version, appliedRanges }
//      或 { ok: false, reason: 'future-version' | 'invalidated' | 'overlapping-ranges' }
```

- 主范围与全部次要光标范围都基于 `item.version` 逐一映射；任一失效则整体拒绝，
  文档保持不变。
- 所有替换合并为**一个事务**提交：`doc.undo()` 一次即回退全部光标的修改。
- 映射后的范围互相重叠（含两个光标重合于一点）时拒绝应用。
- `resolveCompletionRanges` 可只核对/映射而不修改文档（用于高亮预览等）。

## 语义细节

- 只接触边界的删除/替换**不**使范围失效（范围相应平移或收缩）。
- 空范围（光标）上的插入：`Expand` 吸收为新选区，`Collapse` 坍缩到插入之后。
- 空范围被删除覆盖时失效。
- 已知权衡：`Expand` 下边界输入先扩张了范围，再撤销该输入会删除已扩张范围的
  内部，按规则判定失效；如需“撤销后旧响应仍可用”的行为，请使用 `Collapse`。

## 开发

```sh
npm test        # node --test（Node ≥ 23，原生运行 .ts）
```

代码结构：

- `src/range.ts` — 位置/范围穿过单次编辑与编辑序列的映射。
- `src/document.ts` — 版本化文档、编辑链、事务、撤销/重做。
- `src/completion.ts` — 补全响应核对、映射与事务化应用。
