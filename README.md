# folded-text-projection

嵌套折叠区间 → 可见文本投影 + 原文偏移映射。所有公开偏移均为 **UTF-16 code unit** 偏移
（与 LSP / VS Code 一致）。纯 TypeScript，零依赖，使用 Node.js 内置测试运行器验证
（Node ≥ 23 直接执行 `.ts`，无需编译步骤）。

## 运行

```sh
npm test     # node --test，运行 test/folding.test.ts
npm run demo # node examples/demo.ts，运行样例
```

## 功能与规则

- **投影**：`FoldedTextProjection` 把每个"最外层已折叠"区间替换为占位符生成可见文本，
  并维护可见偏移 ↔ 原文偏移的双向映射（`toVisibleOffset` / `toOriginalOffset`）。
- **嵌套**：外层折叠时内层占位符不再显示；展开外层后内层仍保持折叠并显示自己的占位符。
- **部分相交**：默认抛出 `FoldIntersectionError`；`onIntersection: 'merge'` 时把相交簇
  规范化为区间并集，原有被包含的折叠保持嵌套。
- **UTF-16**：折叠边界或编辑边界切开代理对（surrogate pair）时抛出 `SurrogatePairError`；
  表情符号等增补平面字符按 2 个 code unit 计。
- **编辑**：`applyEdit(visibleStart, visibleEnd, text)` 先把可见范围映射回原文
  （覆盖占位符的选择扩展为完整折叠范围），应用编辑后平移/拉伸/移除受影响的折叠，
  幸存折叠的嵌套层级与折叠状态保持不变。
- **选择映射**：`mapVisibleRangeToOriginal` 将跨占位符的选择映射回完整原文范围。
- **行终结符**：`positionAt` / `offsetAt`（`src/position.ts`）同时支持 LF、CRLF 与孤立 CR。

## API 摘要

```ts
const p = new FoldedTextProjection(text, [
  { start: 10, end: 42, placeholder: ' ... ' },   // 外层
  { start: 20, end: 30 },                          // 内层（嵌套）
]);
p.getVisibleText();                 // 投影文本
p.toVisibleOffset(25);              // 原文偏移 -> 可见偏移
p.toOriginalOffset(12);             // 可见偏移 -> 原文偏移
p.mapVisibleRangeToOriginal(8, 15); // 可见选区 -> 完整原文范围
p.applyEdit(0, 0, '// x\n');        // 编辑可见文本，折叠随动
p.setCollapsed(id, false);          // 展开/折叠，嵌套状态保留
p.getFolds();                       // 当前折叠快照（含 depth）
```

## 测试结果

`node --test`（Node v23.9.0）：16 个测试全部通过，覆盖单折叠投影与往返映射、
嵌套折叠的展开/折叠状态机、相交拒绝与合并规范化、非法区间、UTF-16 代理对保护、
跨占位符选择映射、编辑前后折叠平移与嵌套保留、LF/CRLF/混合行尾的位置换算、
跨 CRLF 行折叠、全文本折叠。
