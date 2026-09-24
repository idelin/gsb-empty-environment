# 可分支撤销历史

一个零依赖 TypeScript 实现。撤销后继续编辑会产生新的历史分支；可以在叶分支或任意可见节点之间切换，切换时同时恢复文本和有方向的选择区。

## 行为约定

- 连续纯插入会合并为一个撤销节点；删除、替换、光标移动、事务开始/提交/回滚都会中断合并。
- 显式事务中的多次编辑属于同一个撤销节点，提交前可整体回滚。
- 撤销后再编辑不会丢弃旧路径，而是在当前节点下追加子节点形成撤销树。
- `getBranches()` 返回当前可见叶分支；`switchTo(id)` 可恢复任意可见节点。
- 所有公开偏移均按 JavaScript/TypeScript 的 UTF-16 code unit 计算；编辑范围和选择区都禁止落在代理对中间。
- 不规范化换行，保留并覆盖 `LF` 与 `CRLF` 文本。
- 容量超限时，只回收非叶根节点；其每个存活子分支会先物化为完整文本基线，再升级为根，因此不会破坏存活分支的回放基线。

## 常用 API

```ts
const history = new BranchingHistory({ maxHistoryNodes: 200 });

history.replace(start, end, insertedText, selectionAfterEdit);
history.setSelection({ anchor, active });

history.beginTransaction();
history.replace(0, 0, 'a');
history.commitTransaction();
history.rollbackTransaction();

history.undo();
history.redo(childIndex);
history.getBranches();
history.switchTo(nodeId);
history.switchToBranch(branchIndex);
history.replayVisibleNodes();
```

`selection` 同时包含 `anchor` 和 `active`，因此可以恢复反向选择；省略时默认放到编辑后的插入点。

## 运行样例

无需安装依赖，要求 Node.js 23+：

```bash
npm run demo
```

实际输出：

```text
typed ab: text="ab", anchor=2, active=2, node=n2
undo    : text="", anchor=0, active=0, node=n1
typed x : text="x", anchor=1, active=0, node=n3
branches: [
  { id: 'n2', text: 'ab', selection: { anchor: 2, active: 2 } },
  { id: 'n3', text: 'x', selection: { anchor: 1, active: 0 } }
]
ab branch: text="ab", anchor=2, active=2, node=n2
x  branch: text="x", anchor=1, active=0, node=n3
compact replay: [ { node: 'n2', text: 'ab' }, { node: 'n3', text: 'x' } ]
```

## 内置测试

```bash
npm test
```

测试使用 `node:test` 和 `node:assert/strict`，覆盖：

- `ab` 撤销后输入 `x`，保留 `ab` 与 `x` 两条分支。
- 分支切换时恢复文本和有方向的选择区。
- 连续键入合并；光标移动和显式事务边界后不跨边界合并。
- 事务多次编辑单步撤销和事务回滚。
- UTF-16 偏移、代理对边界、`LF`/`CRLF`。
- 容量压缩后每个可见节点仍可回放，存活分支基线不被破坏。

当前测试结果：

```text
▶ BranchingHistory
  ✔ keeps ab and x as separate branches after undo and continued editing
  ✔ restores directed selections when switching branches
  ✔ coalesces continuous typing but not across cursor movement or transaction boundaries
  ✔ groups multiple changes inside one explicit transaction into one undo step
  ✔ does not coalesce typing after a rolled-back transaction boundary
  ✔ uses UTF-16 code unit offsets and rejects edits that split surrogate pairs
  ✔ supports editing and undo across LF and CRLF text
  ✔ replays every visible node after capacity compaction
  ✔ preserves shared and surviving branch baselines during compaction
  ✔ switches to arbitrary visible nodes and replays their complete state
  ✔ keeps compacted branch baselines replayable and stable on undo
✔ BranchingHistory
ℹ tests 11
ℹ pass 11
ℹ fail 0
```
