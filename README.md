# 可逆编辑撤销树（Undo/Redo CRDT Graph）

协同编辑中的**分布式撤销树**：每个用户可单独撤销/重做自己的操作，而不破坏其他并发协作者的输入。Undo/Redo 不修改历史，而是向操作 DAG 追加**反向补偿节点**，从而维护拓扑有效性。

## 核心设计

- **RGA 字符序列**（`src/rga.ts`）：文档底层是 Replicated Growable Array，删除即墓碑（`visible=false`），删除/恢复均幂等，并发插入按 `(counter, site)` 全序收敛。
- **操作 DAG**（`src/types.ts`）：每个操作（含补偿操作）是 DAG 节点，`parents` 指向创建时的因果头部（heads），`compensates` 指向被补偿节点；历史只增不改。
- **补偿式撤销**（`src/replica.ts`）：
  - `insert` 的逆是 `delete`（墓碑），`delete` 的逆是 `restore`，`restore` 的逆是 `delete`；
  - 每个站点维护自己的 undo/redo 栈，Undo 弹出自己最近的原始操作并追加其逆节点，Redo 对补偿节点再取逆；
  - 由于补偿只作用于操作者自己的字符 id 集合，并发协作者的内容不受影响。
- **因果投递**：远程节点按 `parents` 做因果缓冲，保证拓扑序集成。

## 运行

需要 Node.js ≥ 22.6（内置 `node:test` + `--experimental-strip-types`，零依赖，无需编译）。

```bash
npm test        # 运行 Node.js 内置测试（node:test）
npm run demo    # 运行验收场景样例
```

## 验收场景（`npm run demo` 实际输出）

```
1) A 输入 hello 并同步
  A 视图: "hello"   B 视图: "hello"
2) B 在末尾插入 ! 并同步
  A 视图: "hello!"   B 视图: "hello!"
3) A 执行 Undo（追加补偿节点 node:A:7）
  A 视图: "!"   B 视图: "!"
4) A 执行 Redo（追加补偿节点 node:A:8）
  A 视图: "hello!"   B 视图: "hello!"

DAG 节点（拓扑序，parents 为因果依赖）:
  node:A:2  A:insert   parents=[]
  node:B:2  B:insert   parents=[node:A:2]
  node:A:7  A:delete   parents=[node:B:2]  compensates=node:A:2
  node:A:8  A:restore  parents=[node:A:7]  compensates=node:A:7
```

## 测试结果（`npm test`）

```
ok 1 - 验收场景：A 输入 hello，B 插入 !，A 撤销后保留 !，重做恢复 hello!
ok 2 - 撤销树只追加节点：Undo/Redo 生成反向补偿节点且历史不被修改
ok 3 - 多次撤销按后进先出回退自己的操作
ok 4 - 撤销删除操作：delete 的补偿是 restore
ok 5 - 并发插入收敛：双方离线输入后同步结果一致
ok 6 - DAG 拓扑有效性：每个节点的父节点先于其存在
# tests 6  # pass 6  # fail 0
```

## 目录结构

- `src/types.ts` — 操作节点 / DAG 类型定义
- `src/rga.ts` — RGA 字符序列（墓碑删除、并发收敛）
- `src/replica.ts` — 副本：DAG 维护、Undo/Redo 补偿、因果同步
- `test/undo-redo-crdt.test.ts` — Node 内置测试（含验收场景）
- `examples/demo.ts` — 可运行样例
