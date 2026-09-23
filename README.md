# path-sub-patch-stream

按路径订阅的补丁流（TypeScript，零依赖，Node.js 内置测试运行器验证）。

## 设计

- **共享自动机**（`src/automaton.ts`）：所有订阅模式编译进同一棵段 trie。
  字面量边跨订阅共享，每个状态至多一条 `*` 通配边；`/**` 表示任意深度。
  匹配沿路径同时走字面量边和 `*` 边，在每个前缀状态收集订阅（祖先语义），
  并返回实际遍历的状态数 `visited`。
- **祖先/通配匹配**（`src/pattern.ts`）：模式是路径的前缀即命中，因此订阅
  父路径会收到子改动；比较按段进行，相邻字符串前缀（`/a/b` vs `/a/bc`）
  不是祖先。`*` 匹配恰好一段。
- **事务与补丁**（`src/store.ts`）：事务在私有草稿上操作，提交时原子换根，
  读者只能看到已提交（且深冻结）的快照，看不到中间态。操作全部记入
  journal，可 `replay()` 重放。`move` 展开为旧路径 delete + 新路径 set
  （同一 opId）；数组删除/插入产生带 `range=[lo..hi]` 的 splice 补丁，
  明确覆盖所有位移下标。
- **去重与顺序**：同一订阅在一次提交中只通知一次；`patches` 按操作顺序
  排列，`opIds` 去重但保留操作身份与顺序。
- **事务边界**：事务内（或通知回调中）的 `subscribe`/`unsubscribe` 暂存，
  提交边界生效——本次通知仍发给事务开始时的订阅集合；通知回调里的重入
  `transact` 排队，本轮投递结束后才提交。
- **参考核验**（`src/reference.ts`）：独立的逐订阅朴素匹配器，测试用它
  交叉核验自动机派发结果，并对比朴素比较次数与自动机遍历状态数。

## 运行

```sh
npm test    # node --test tests/*.test.ts（Node >= 22.6，原生类型擦除）
npm run demo
```

## 运行样例（`npm run demo` 节选）

```
== tx1 -> /users ==
  sub#1 /users  ops=[1,2]
    set    /users/u1/email
    delete /users/u1/name movedTo=/archive/u1-name
== tx1 -> /queue ==
  sub#3 /queue  ops=[3]
    splice /queue range=[1..3]
== tx1 -> /archive ==
  sub#4 /archive  ops=[2]
    set    /archive/u1-name movedFrom=/users/u1/name
replay matches: true
journal size: 5 ops, automaton states: 8
```

## 测试结果（`npm test`，19 通过 / 0 失败）

- 父路径收到子改动；相邻字符串前缀不是祖先；`*`/`**` 通配。
- 移动同时通知旧/新路径且共享同一 opId。
- 重复匹配去重为一次通知，操作顺序与身份保留。
- 数组删除/插入明确通知位移下标范围（如 `range=[1..4]`）。
- 订阅增删与重入写均在事务边界生效；读者看不到事务中间态。
- journal 重放得到一致状态；自动机派发与逐订阅参考匹配完全一致。
- 遍历状态实测：201 个共享前缀订阅、深度 9 的路径，自动机仅遍历
  **13** 个状态（朴素逐订阅约 **1608** 次比较）；混合事务自动机遍历
  **27** 状态 vs 参考 **128** 次比较。
