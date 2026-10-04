# persistent-multiset

可分支的持久化有序多重集（TypeScript 实现）。

## 特性

- **版本化 / 可分支**：`insert` / `remove` 基于某个版本执行并返回**新版本**，旧版本永远不变，任意旧版本都可以继续作为分支起点；`fork(v)` 提供 O(1) 的显式分支。
- **多重集语义**：重复值按计数存储（`count(v, key)`），`size` 计入重复。
- **rank / select**：`rank(v, key)` = 严格小于 `key` 的元素个数；`select(v, k)` 返回从 **0** 开始的第 k 小元素。
- **明确报错**：空集 `select` 抛 `EmptyMultisetError`；序号越界/非整数抛 `RangeError`；删除不存在的键抛 `KeyNotFoundError`；使用未知或已释放版本抛 `UnknownVersionError`。
- **结构共享 + 引用计数 GC**：节点不可变、版本间共享，旧版本计数不受新版本影响；`release(v)` 只回收从所有存活版本都不可达的节点（`liveNodeCount()` / `reachableNodeCount()` 可观测）。

## 实现

路径复制（path copying）的 AVL 树。所有操作 O(log n)。每次修改只复制根到修改点的路径（含旋转涉及节点），其余子树在版本间共享；每个节点记录引用计数（父指针 + 版本根），释放版本时递归回收计数归零的子树。

## API

```ts
const ms = new PersistentMultiset();
const v0 = ms.create();              // 空版本
const v1 = ms.insert(v0, 5);         // 基于 v0 插入，返回新版本
const v2 = ms.insert(v1, 5);         // 重复值计数
const v3 = ms.remove(v1, 5);         // 从旧版本 v1 继续分支，删除一份
ms.rank(v2, 5);                      // 0（小于 5 的元素个数）
ms.select(v2, 1);                    // 5（0 起始）
ms.count(v2, 5);                     // 2
ms.toArray(v3);                      // [5]
ms.release(v1);                      // 只回收不再被任何版本引用的节点
```

## 构建与测试

仓库内已附带 TypeScript 编译器（`node_modules/typescript`，离线可用）：

```sh
npm test   # 编译 + 运行确定性检查与随机模型对照测试
```

`test/model-test.ts` 以排序数组为 oracle：随机插入/删除/rank/select/分支/重复释放（5 个种子 × 3000 步），每次释放后校验 `liveNodeCount() === reachableNodeCount()`，并周期性全量比对所有存活版本与 AVL 不变量，最终释放全部版本后存活节点数归零。
