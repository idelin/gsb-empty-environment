# attr-ranges

富文本属性区间库（TypeScript，零依赖）。支持三类属性区间，文本编辑后按各自
冲突规则拆分/合并，序列化结果规范且与插入顺序无关。

要求 Node.js >= 23.6（利用内置 TypeScript 类型擦除直接运行 `.ts`，无需编译）。

## 属性区间与冲突规则

| 属性 | 规则 |
| --- | --- |
| `bold` | 可合并行内标记。重叠或相邻的同属性粗体自动合并为一个区间。 |
| `link` | 新区间覆盖冲突：新链接拥有其范围，旧链接保留不重叠的残段。 |
| `entity` | 不可重叠、不可分割。添加重叠实体抛错；编辑触及实体内部则实体整体失效，并通过 `EditResult.invalidations` 给出原因（`entity-interior-edited` / `entity-deleted`）。 |

编辑时的区间映射规则（bold / link 可拆分，entity 不可拆分）：

- 插入点在区间**严格内部** → 区间扩张覆盖新文本；在边界上 → 不扩张。
- 删除/替换区间被标记**严格包含** → 标记伸展覆盖新文本。
- 删除/替换与标记**部分重叠** → bold/link 保留重叠区外的残段（可拆成两段）；
  entity 整体失效并报告原因。
- 删除完全覆盖 entity → 实体随文本删除，报告 `entity-deleted`。

## UTF-16 偏移与代理对

- 所有公开偏移均为 UTF-16 码元偏移（即 JS 字符串下标）；CRLF 占 2 个码元，
  LF 占 1 个，二者都支持。
- 任何会切开代理对的偏移都会向外吸附到码点边界：插入点/区间起点向下取整，
  区间终点向上取整（`snapPoint` / `snapRangeStart` / `snapRangeEnd`）。

## 规范序列化

`serialize()` / `toCanonicalJSON()` 输出与插入顺序无关的规范形式：

- 丢弃空区间，粗体合并；
- 标记按 `(start, end, type, payload)` 全序排序；
- JSON 对象键（含 entity `data`）递归排序。

`RichTextDocument.deserialize()` 可从序列化结果无损还原（往返稳定）。

## API 摘要

```ts
const doc = new RichTextDocument('hello @alice world');
doc.addBold(0, 5);                        // 粗体（自动合并）
doc.addLink(0, 5, '/hi');                 // 链接（覆盖冲突区间）
doc.addEntity(6, 12, 'mention', { id: 'u1' }); // 实体（重叠抛错）

doc.insert(9, 'x');                       // => { invalidations: [ { reason: 'entity-interior-edited', ... } ] }
doc.remove(0, 2);
doc.replace(0, 2, 'hi');

doc.serialize();                          // 规范对象
doc.toCanonicalJSON();                    // 规范 JSON 字符串
RichTextDocument.deserialize(doc.serialize());
```

## 运行

```sh
npm test      # Node 内置测试运行器（node:test）
npm run demo  # 运行样例
```

## 运行样例输出（`npm run demo`）

```text
=== 1. adjacent bolds merge ===
text     : "abcdefghij"
canonical: {"marks":[{"end":10,"start":0,"type":"bold"}],"text":"abcdefghij"}

=== 2. link override keeps remnants ===
text     : "hello world"
canonical: {"marks":[{"end":4,"href":"/old","start":0,"type":"link"},{"end":8,"href":"/new","start":4,"type":"link"},{"end":10,"href":"/old","start":8,"type":"link"}],"text":"hello world"}

=== 3. entity interior edit invalidates ===
text     : "hello @alxice world"
canonical: {"marks":[],"text":"hello @alxice world"}
invalidations: [
  {
    "entity": { "type": "entity", "start": 6, "end": 12, "kind": "mention", "data": { "id": "u1" } },
    "reason": "entity-interior-edited",
    "detail": "insert of 1 char(s) at offset 9 falls inside entity [6, 12)"
  }
]

=== 4. surrogate pair safety ===
snapPoint(2)   : 1 (mid-pair offset 2 -> 1)
snapRangeEnd(2): 3 (mid-pair offset 2 -> 3)

=== insert at mid-pair offset ===
text     : "ax😀b"

=== 5. LF + CRLF edits ===
text     : "abcd\nef"
canonical: {"marks":[{"end":3,"start":1,"type":"bold"}],"text":"abcd\nef"}

=== 6. insertion-order independence ===
doc A: {"marks":[{"end":5,"start":0,"type":"bold"},{"end":6,"href":"/x","start":2,"type":"link"},{"end":9,"start":6,"type":"bold"},{"end":12,"kind":"tag","start":10,"type":"entity"}],"text":"abcdefghijkl"}
doc B: {"marks":[{"end":5,"start":0,"type":"bold"},{"end":6,"href":"/x","start":2,"type":"link"},{"end":9,"start":6,"type":"bold"},{"end":12,"kind":"tag","start":10,"type":"entity"}],"text":"abcdefghijkl"}
identical: true
```

## 测试结果（`npm test`，node:test，23 个用例全部通过）

```text
✔ adjacent bold ranges with same attributes merge
✔ overlapping bold ranges merge
✔ bold ranges separated by a gap stay distinct
✔ insert inside bold expands it; at boundaries it does not
✔ deleting all marked text drops the empty mark
✔ overlapping link overrides and keeps old link remnants
✔ new link fully covering an old link replaces it
✔ replace inside a link stretches it over the new text
✔ overlapping entities are rejected
✔ edit inside an indivisible entity invalidates it with a reason
✔ delete partially overlapping an entity invalidates the whole entity
✔ delete fully covering an entity reports entity-deleted
✔ edits at entity boundaries leave the entity intact
✔ insert offset inside a surrogate pair snaps before the pair
✔ remove range cutting a surrogate pair expands to the whole pair
✔ mark range cutting a surrogate pair snaps outward
✔ edits around emoji keep offsets in UTF-16 units
✔ CRLF counts as two UTF-16 units; marks and edits work across lines
✔ mixed LF/CRLF text survives a serialize round trip byte-identically
✔ canonical serialization is independent of insertion order
✔ canonical JSON sorts entity data keys recursively
✔ serialize/deserialize round trip is stable
✔ marks getter returns copies, not live references
ℹ tests 23
ℹ pass 23
ℹ fail 0
```

## 目录结构

```text
src/document.ts   核心实现（RichTextDocument）
src/index.ts      类型与类导出
test/document.test.ts  node:test 测试（23 例）
examples/demo.ts  运行样例
```

> 注：仓库附 `tsconfig.json` 供编辑器与 `tsc --noEmit` 类型检查使用；
> 运行与测试依赖 Node 内置类型擦除，无需安装 TypeScript。
