# composition-editor

TypeScript 实现的组合输入（IME composition）编辑状态机。零依赖，直接由
Node.js（>= 22.6，类型擦除）运行，测试使用 Node 内置测试运行器。

## 核心设计

- **双状态车道**：正式版本（formal document + 撤销历史）与临时投影
  （projection = 正式文本叠加组合区间内容）。组合中间态只存在于投影，
  绝不进入正式文档与撤销历史。
- **代际 ID**：每次 `compositionStart()` 递增代际；`update/commit/cancel`
  携带代际 ID，过期回调一律返回 `false` 且无副作用。
- **一次提交 = 一个撤销单元**：任意多次 update 后 commit 只产生一条历史。

## 公开规则（确定性）

- **R1** 公开偏移为 UTF-16 码元下标；会切开代理对的偏移向下吸附到代理对
  起点（`src/utf16.ts`）。LF 与 CRLF 原样存储（CRLF 计 2 个码元）。
- **R2** 过期代际的回调是空操作，旧回调不可污染新组合。
- **R3** update 只写投影；commit 把最终文本作为**唯一**撤销单元写入正式
  文档；重复 commit / 迟到 update 不会二次插入。
- **R4** cancel 恢复组合前文本与选区，不产生历史条目，绝不撤销远端内容。
- **R5** 外部编辑（remote/local/undo/redo）与组合区间 `[start,end)` 的
  冲突规则：编辑区间 `[s,e)` 满足 `s < end && start < e` 即冲突 ——
  **外部编辑获胜，组合被取消**，光标落到外部编辑末尾；否则组合范围按
  OT 规则变换。恰好在边界上的纯插入不算冲突：起点边界插入把范围整体
  前推，终点边界插入不影响范围，点状组合整体前移。
- **R6** 组合中再次 `compositionStart()` 取消旧组合；失焦（`blur()`）
  取消组合。
- **R7** undo/redo 命中组合区间时同样按 R5 变换或取消。

## API 摘要（`src/editor.ts`）

```
new Editor(text, selection?)
compositionStart(): number              // 返回代际 ID
compositionUpdate(id, text): boolean
compositionCommit(id, text?): boolean   // 缺省提交当前投影文本
compositionCancel(id): boolean
blur(): void
applyLocalEdit({start,end,text})        // 本地编辑，各占一个撤销单元
applyRemoteEdit({start,end,text})       // 远端编辑，冲突按 R5
undo(): boolean / redo(): boolean
getText() / getProjection() / getSelection() / isComposing()
getCompositionRange() / canUndo() / canRedo() / historyKinds()
```

## 运行

```
node examples/demo.ts   # 运行样例（5 个场景）
npm test                # 等价于 node --test，16 个测试
```

## 验证方式

- `test/editor.test.ts`：针对每条规则的单元测试（十次更新一个撤销单元、
  取消恢复文本与选区、重复 commit/迟到 update 不二次插入、代理对、
  LF/CRLF、代际隔离、冲突取消、边界插入、undo/redo 交错等）。
- `test/exploration.test.ts`：有限状态探索 —— 对 12 个事件组成的字母表
  穷举所有长度 ≤ 4 的序列（2 万余条），外加 300 个确定性种子的长度 15
  随机序列；每个事件后校验：与独立影子模型的正式文本/投影一致、过期
  回调被拒绝、commit 每代际至多一次、cancel 不改变正式文本（故不可能
  撤销远端内容）、撤销历史可精确往返且不含组合中间态。

## 测试结果（Node v23.9.0）

```
ℹ tests 16
ℹ pass 16
ℹ fail 0
```
