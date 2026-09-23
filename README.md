# monotone-ring

声明式单调有限格计算环：在有限格上声明互相依赖（允许成环）的单元，
通过混沌不动点迭代（chaotic iteration）求最小不动点。不依赖任何响应式框架。

## 特性

- **有限格**：`Lattice<T>` 接口（`bottom` / `leq` / `join` / `equals` / 有限载体
  枚举 `elements()`），内置布尔格与幂集格。
- **单调性校验**：声明的函数在求解前对依赖格的有限笛卡尔积做穷举校验
  （`a <= b` 蕴含 `f(a) <= f(b)`）。非单调环（如 `x = NOT x`）在迭代开始前
  即被拒绝，不会无限空转。组合规模超限时可显式 `assumeMonotone` 跳过静态校验，
  运行时仍有"值只升不降"守卫兜底。
- **不动点迭代**：从 `bottom` 出发的上升混沌迭代，worklist 调度顺序可插拔
  （`fifo` / `lifo` / 带种子 `random`），任意顺序收敛到同一最小不动点。
- **迭代预算**：`budget` 限制求值次数，耗尽时返回 `status: "exhausted"`、
  `converged: false`，标记未收敛。
- **事务存储**：`Store` 提供事务（工作副本 + 原子提交），读者只能拿到提交后的
  不可变快照，永远看不到事务中间态；所有写操作追加到 op log，
  `Store.replay(log)` 可完整回放重建状态。求解器把整轮迭代的写入放在单个
  事务里，提交前读者看到的仍是旧值。

## 运行

需要 Node.js >= 23.6（原生运行 TypeScript）。Node 22.6+ 需加
`--experimental-strip-types`。只用 Node 内置模块，无第三方依赖。

```sh
npm test    # node --test（内置测试运行器）
npm run demo
```

## 结构

- `src/lattice.ts` — 有限格抽象、布尔格、幂集格
- `src/system.ts` — 计算环声明、单调性校验、不动点求解器
- `src/store.ts` — 事务存储、快照隔离、可回放 op log
- `test/` — `node:test` 测试
- `examples/demo.ts` — 运行样例
