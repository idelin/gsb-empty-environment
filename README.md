# Atomic Multi-Cursor Edit Transactions

TypeScript 实现的原子多光标编辑事务（零依赖，Node.js ≥ 23.6 原生运行 TS）。

## 设计要点

- **同一基线坐标系**：事务内所有 `replace/insert/delete` 的范围都从事务开始时的文本（基线）解释，编辑之间互不影响坐标。提交时按起点升序单趟应用，后方替换的长度变化永远不会使前方范围错位。
- **冲突写入前失败**：提交时先排序并检查重叠（`curr.start < prev.end`），任何重叠替换在写入前抛出 `EditConflictError`；越界、反转区间在 `replace()` 调用时即抛出 `EditOutOfBoundsError`。
- **UTF-16 公开偏移**：所有偏移为 UTF-16 码元下标（与 JS 字符串一致）。落在高/低代理项之间的编辑边界抛出 `SurrogatePairError`；首字符为孤立低代理项或尾字符为孤立高代理项的插入文本同样被拒绝。CRLF 计 2 个偏移，`positionAt/offsetAt` 同时支持 LF、CRLF、CR。
- **四类状态原子更新**：文本、选择区、标注、撤销记录在同一事务内提交；回调抛错或校验失败时，四者全部恢复到事务前的精确快照（撤销栈深度也会截断恢复）。
- **位置映射**：提交后选择区与标注按编辑集重映射——折叠光标随插入文本前移（打字语义），非折叠选择区覆盖被替换区域的新文本；标注起点用 start 偏置、终点用 end 偏置。
- **撤销**：每个有实际变更的成功事务恰好压入一条撤销记录，`undo()` 一步恢复文本 + 选择区 + 标注。

## API 概览

```ts
const doc = new EditorDocument("foo 😀 bar\r\nbaz quux");
doc.setSelections([{ anchor: 0, head: 3 }]);
doc.setAnnotations([{ id: "review", range: { start: 16, end: 20 } }]);

doc.transact((tx) => {
  tx.replace(0, 3, "FOO");      // 基线坐标
  tx.replace(4, 6, ":smile:");  // 替换整个代理对
  tx.insert(12, ">> ");         // insert / delete 是 replace 的便捷形式
  tx.setSelections([...]);      // 同样按基线坐标解释并自动重映射
  tx.addAnnotation({ id: "x", range: { start: 0, end: 3 } });
});

doc.undo();           // 恢复上一事务前的全部状态
doc.positionAt(5);    // { line, character }
doc.offsetAt({ line: 1, character: 0 });
```

错误类型：`EditConflictError`、`SurrogatePairError`、`EditOutOfBoundsError`、`NestedTransactionError`、`TransactionStateError`。

## 运行

```bash
npm test    # node --test（Node.js 内置测试运行器）
npm run demo
```

## 运行样例（`node examples/demo.ts` 摘要）

```
=== before transaction ===
text:        "foo 😀 bar\r\nbaz quux"
selections:  [{"anchor":0,"head":3},{"anchor":12,"head":15}]
annotations: [{"id":"review","range":{"start":16,"end":20}}]

=== after multi-cursor transaction ===
text:        "FOO :smile: bar\r\nBAZ QUUX!!"
selections:  [{"anchor":0,"head":3},{"anchor":17,"head":20}]
annotations: [{"id":"review","range":{"start":21,"end":27}}]
undo depth:  1

=== overlapping edits rejected ===
EditConflictError: overlapping edits: [0, 3) and [2, 4)
（状态与事务前完全一致，undo depth 仍为 0）

=== throwing callback rolled back ===
caught: callback blew up
（文本/选择区/标注/撤销栈四类状态全部恢复）

=== surrogate split rejected ===
SurrogatePairError: replace start 5 splits a surrogate pair
```

## 测试结果（`node --test`）

```
ℹ tests 24
ℹ pass 24
ℹ fail 0
```

覆盖：基线坐标系多替换、后方长度变化不影响前方、重叠写入前失败、相邻/同点插入、代理对切分拒绝（含插入文本边界）、越界拒绝、LF/CRLF 行偏移与跨 CRLF 编辑、选择区/标注重映射、回调抛错四类状态全恢复、校验失败中途恢复、单事务单撤销记录、undo 全状态恢复、空事务不入栈、嵌套事务拒绝、emoji+CRLF 多光标组合。
