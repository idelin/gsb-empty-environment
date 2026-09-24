# rich-text-attrs

带属性区间的富文本文档模型（TypeScript，零依赖，Node.js 内置测试运行器验证）。

## 模型

- **UTF-16 偏移**：所有公开偏移都是 UTF-16 码元下标（即 JS 字符串下标）。
  任何编辑或属性设置都不得切开代理对，违反时抛出 `RangeError`。
  LF 计 1 个码元，CRLF 计 2 个码元，均按普通文本处理。
- **三类属性区间**（半开区间 `[start, end)`）：
  - `bold`：布尔属性，可自由重叠；同属性相邻/重叠区间自动合并。
  - `link`：互斥。新链接总是完整覆盖其范围，旧链接保留不重叠的残段
    （可能被切成两段）；同 href 的相邻残段在规范化时合并。
  - `entity`：互斥且不可分割。在实体内部插入、或删除范围与实体部分相交，
    整个实体失效并返回带原因的事件；新区间与旧实体相交时旧实体整体被替换。
    完全覆盖实体的删除使其随文本一并移除（不产生失效事件）。

## 编辑语义

- `insert(offset, text)`：区间边界处的插入不进区间；区间内部的插入扩展
  bold/link 区间；实体内部插入使其整体失效（`insert-inside-entity`）。
- `delete(start, end)`：bold/link 区间被裁剪、右段左移；与实体部分相交
  使其整体失效（`delete-intersects-entity`）。
- 每个编辑方法返回 `EditEvent[]`，实体失效事件携带原因：
  `insert-inside-entity` / `delete-intersects-entity` / `replaced-by-new-entity`。

## 规范化序列化

`serialize()` 输出规范 JSON：固定键序、区间按 `(start, end)` 排序、
bold 合并、同 href 相邻链接合并。同一逻辑状态无论操作顺序如何，
序列化结果逐字节相同。

## 使用

```ts
import { RichTextDocument } from "./src/richtext.ts";

const doc = new RichTextDocument("hello @alice");
doc.setBold(0, 5);
doc.setEntity(6, 12, "mention", "u1");
const events = doc.insert(8, "X"); // 实体内部编辑
// events = [{ type: "entity-invalidated", reason: "insert-inside-entity", ... }]
console.log(doc.serialize());
```

## 运行

```sh
npm test    # node --test（Node >= 23.6 原生运行 TypeScript）
npm run demo
```
