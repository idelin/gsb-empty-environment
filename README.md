# persistent-multiset

可分支的持久化有序多重集（TypeScript 实现）。基于路径复制的 AVL 树：
版本间结构共享，任何修改都不会影响旧版本；引用计数回收不可达节点。

## 特性

- `insert(value)` / `remove(value)`：插入 / 删除一份（O(log n)，路径复制）
- `rank(value)`：严格小于 `value` 的元素个数
- `select(k)`：第 k 小元素，**从 0 开始**
- `fork()`：O(1) 从任意旧版本分支，共享全部节点
- `release()`：释放版本，只回收不再被任何版本可达的节点
- 重复值按计数存储（`countOf` 查询重数）
- 明确错误：`EmptyMultisetError`、`IndexOutOfRangeError`、
  `ValueNotFoundError`、`ReleasedVersionError`（重复释放 / 释放后使用）

## 用法

```ts
import { PersistentMultiset } from "./src/multiset.ts";

const v1 = new PersistentMultiset<number>();
v1.insert(3); v1.insert(1); v1.insert(3);

const v2 = v1.fork();      // 从旧版本分支
v2.remove(3);              // 只删一份；v1 不受影响

v1.select(0);              // 1
v1.rank(3);                // 1（小于 3 的元素个数）
v1.countOf(3);             // 2
v2.toArray();              // [1, 3]

v1.release();              // 回收仅 v1 可达的节点
v2.release();
```

泛型版本可传自定义比较器：`new PersistentMultiset<T>((a, b) => ...)`。

## 运行测试

需要 Node.js >= 23.6（内置 TypeScript 类型擦除），或先用 tsc 编译：

```sh
npm test
```

测试以排序数组为模型做随机化核对（插入、删除、rank、select、fork、
重复释放），并校验每个存活版本可达节点的并集与节点池存活计数相等，
全部版本释放后节点归零。
