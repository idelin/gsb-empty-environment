# monotone-fixpoint-engine

声明式**单调有限格计算环**引擎：通过混沌不动点迭代（chaotic iteration）求稳定值。
不依赖任何响应式框架，仅 Node.js 内置模块；TypeScript 编写，Node >= 23.6 直接运行（类型擦除）。

## 设计

- **有限格**（`src/lattice.ts`）：值域接口 `Lattice<T>`（`bottom` / `leq` / `join` / `eq`），
  内置布尔格与有限幂集格。所有节点值只沿格序单调上升，有限格保证必然终止。
- **不动点引擎**（`src/engine.ts`）：
  - 派生节点必须声明 `monotone: true` 才能参与（允许成环），否则声明期拒绝；
  - `settle()` 做混沌迭代：工作集调度（fifo / lifo / priority），任一公平调度都收敛到同一最小不动点；
  - 运行时守卫：新值若不 `>=` 旧值，立即抛 `NonMonotoneError` —— `x = NOT x` 两步内被拒绝，不会无限振荡；
  - 迭代预算耗尽返回 `status: 'non-converged'`，绝不死循环；
  - 输入节点 join 语义（只升不降），每次值变更记录历史链供审计。
- **事务存储**（`src/store.ts`）：
  - 事务写先进私有缓冲，读者只读最近提交并收敛后的快照，看不到中间态；
  - 提交 = 应用全部写 -> 一次 settle -> 版本 +1 -> 原子换快照；
  - 每次提交追加可序列化操作日志，`replay()` 在全新引擎上逐事务重放得到相同状态。

## 运行

```sh
npm test     # Node 内置测试运行器，19 个用例
npm run demo # 四个样例
```

无第三方依赖；如安装 TypeScript 可 `npm run typecheck`（`erasableSyntaxOnly`，与 Node 类型擦除兼容）。

## 运行样例（`npm run demo` 实际输出）

```text
=== 样例 1：布尔可达闭包（带环图）===
状态: converged，迭代次数: 5
  reach(s) = true
  reach(a) = true
  reach(b) = true
  reach(c) = true
  reach(d) = false
a 的上升链: [false -> true]（单调不减）
=== 样例 2a：x = NOT x（谎报单调）===
  已拒绝（NonMonotoneError）：node "x" violated declared monotonicity after 1 iteration(s): value would descend from true to false
  系统在有限步内终止，没有无限振荡
=== 样例 2b：迭代预算耗尽 ===
  链长 50，预算 10 -> 状态: non-converged，已用迭代: 10
=== 样例 3：调度顺序不影响最小不动点 ===
  fifo     状态=converged 迭代=7 值=[["a",true],["b",true],["c",true],["d",false],["e",true],["s",true]]
  lifo     状态=converged 迭代=10 值=[["a",true],["b",true],["c",true],["d",false],["e",true],["s",true]]
  priority 状态=converged 迭代=7 值=[["a",true],["b",true],["c",true],["d",false],["e",true],["s",true]]
  三种调度结果一致 ✔
=== 样例 4：事务与回放 ===
初始: version=0 a=false c=false
事务中（未提交）: a=false（读者仍看到旧快照）
提交 1 后: version=1 a=true c=true
提交 2 后: version=2 b=true c=true
操作日志: [{"tx":1,"ops":[{"type":"set","cell":"s1","value":true}]},{"tx":2,"ops":[{"type":"set","cell":"s2","value":true}]}]
回放状态与当前状态一致: 是 ✔
```

## 测试结果（`npm test` 实际输出）

```text
✔ 无环链收敛
✔ 单调环（布尔可达闭包）收敛到最小不动点
✔ 收敛过程中值沿格序单调不减
✔ x = NOT x 被拒绝，且不无限振荡
✔ 未声明单调的派生节点在声明期被拒绝
✔ 依赖不存在的节点在 settle 时被拒绝
✔ 迭代预算耗尽标记 non-converged
✔ 预算充足时同一链收敛
✔ 调度顺序不同仍得同一最小不动点
✔ 输入节点 join 语义：只升不降
✔ 写派生节点被拒绝
✔ 幂集格上的单调闭包收敛
✔ 布尔格：序与并
✔ 幂集格：子集序与并集
✔ 读者看不到事务中间态
✔ 事务原子提交：一次版本跳动，全部写同时可见
✔ 操作日志可回放：重放后状态一致
✔ 日志按提交顺序记录事务
✔ 事务关闭后不可再写或重复提交
ℹ tests 19
ℹ pass 19
ℹ fail 0
```

## 目录

- `src/lattice.ts` — 格接口、布尔格、幂集格
- `src/engine.ts` — 不动点引擎（调度器、单调守卫、迭代预算）
- `src/store.ts` — 事务、操作日志、回放
- `examples/` — 四个可运行样例
- `test/` — `node:test` 测试
