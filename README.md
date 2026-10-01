# undotree — Undo/Redo CRDT Graph (Go)

协同编辑中的分布式可逆撤销树。每次编辑都是操作图中的一个不可变节点；
Undo/Redo 不删除历史，而是追加引用目标节点的**反向补偿操作节点**，
从而保持图的拓扑有效性，且只撤回本人操作、不破坏并发协作者的输入。

## 设计

- **操作图（append-only）**：`OpInsert` / `OpUndo` / `OpRedo` 三类节点，
  按 Lamport 时钟因果排序；Undo/Redo 通过 `Target` 指向被补偿的插入节点。
- **字符序列（RGA）**：每个字符链接到左邻字符，兄弟节点按 ID 确定性排序，
  并发插入在所有副本上收敛到同一顺序。
- **可见性回放**：渲染时重放操作图，Undo 使目标插入失效、Redo 恢复；
  被撤回字符的子字符仍然可达，协作者的锚点始终有效。
- **副本合并**：`Apply` 幂等、`Merge` 可交换，满足 CRDT 收敛性。

## 运行样例

```sh
go run ./cmd/demo
```

输出：

```
init                               replicaA="" replicaB=""
A inserts "hello"                  replicaA="hello" replicaB="hello"
B inserts "!"                      replicaA="hello!" replicaB="hello!"
A undoes (compensation node)       replicaA="!" replicaB="!"
A redoes (compensation node)       replicaA="hello!" replicaB="hello!"

operation graph on replica A (append-only, causal order):
  A#1 Insert("hello")@1
  B#1 Insert("!")@2
  A#2 Undo(A#1)@3
  A#3 Redo(A#1)@4
```

## 测试

```sh
go test ./... -v
```

覆盖验收边界（A 输入 `hello`、B 插入 `!`、A Undo 后保留 `!`、Redo 恢复
`hello!`），以及按用户撤销、并发插入收敛、撤销/重做栈、图只追加等性质。
