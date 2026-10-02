# Undo/Redo CRDT Graph — 可逆编辑撤销树

协同编辑中的**分布式撤销树**（TypeScript 实现，零依赖）。每个用户可以单独撤销/重做
自己的操作，而不会破坏其他并发协作者的输入。

## 核心设计

- **操作即节点，历史只增不改**：所有编辑（insert / delete / restore）都是追加到
  DAG 上的操作节点，节点通过 `parents`（创建时的图头部）构成因果序。
- **Undo = 反向补偿节点**：撤销从不删除历史，而是追加一个语义相反的补偿节点
  （`insert → delete`、`delete → restore`、`restore → delete`），并通过
  `compensates` 指针指向被补偿的原始操作，维持拓扑有效性。
- **按用户独立的 undo/redo 栈**：`undo(author)` 只弹出该用户自己的最近操作并生成
  补偿节点，因此用户 A 的撤销不会触碰用户 B 并发输入的内容。
- **RGA 风格墓碑**：被删除的元素保留为墓碑，仍是其他并发插入的锚点；
  文档内容通过对操作图做确定性拓扑排序（Kahn + 字典序就绪集）后重放得到，
  保证多副本收敛。
- **分布式接收**：`receive(op)` 支持乱序投递——父节点缺失的操作进入缓冲区，
  因果依赖到齐后自动按序生效。

## 环境要求

Node.js ≥ 22.6（利用原生 TypeScript 类型擦除直接运行 `.ts`，无需构建、无需安装依赖）。

## 运行

```bash
npm test     # node --test test/undoTree.test.ts
npm run demo # node examples/demo.ts
```

## 验收场景

```ts
const doc = new UndoTree('r1');
doc.insert('A', null, 'hello');        // 文档: "hello"
doc.insert('B', doc.tailElementId(), '!'); // 文档: "hello!"
doc.undo('A');                          // 文档: "!"   —— B 的输入被保留
doc.redo('A');                          // 文档: "hello!"
```

对应的操作图（`doc.describe()` 输出）：

```
op#r1:1 [A] insert "hello" (el#r1:0) after <root> parents=[]
op#r1:2 [B] insert "!" (el#r1:1) after el#r1:0 parents=[op#r1:1]
op#r1:3 [A] delete el#r1:0 parents=[op#r1:2] compensates=op#r1:1
op#r1:4 [A] restore el#r1:0 parents=[op#r1:3] compensates=op#r1:3
```

`op#r1:3` 是 `op#r1:1` 的反向补偿节点：它只墓碑化 A 的 `hello` 元素，
B 的 `!` 元素（锚定在被墓碑化的 `hello` 之后）依然可见；`op#r1:4` 再补偿
`op#r1:3`，无缝恢复为 `hello!`。

## 测试覆盖（node:test，11 个用例）

- 验收场景：A 撤销 `hello` 保留 B 的 `!`，redo 恢复 `hello!`
- 补偿节点的拓扑属性（parents / compensates / heads 推进）
- 图拓扑有效性校验（父引用可解析、无环）
- 空历史 undo/redo 安全 no-op
- 同用户连续多步撤销
- 新编辑清空 redo 栈
- 多用户独立撤销互不影响
- 显式 delete 的 undo 恢复
- 墓碑元素作为并发插入锚点
- 双副本操作交换后状态收敛
- 乱序投递缓冲与因果重放

## 文件结构

```
src/undoTree.ts        核心实现（UndoTree 类 + 操作节点类型）
test/undoTree.test.ts  Node.js 内置测试（node:test + node:assert）
examples/demo.ts       验收场景 + 操作图 + 双副本收敛演示
```
