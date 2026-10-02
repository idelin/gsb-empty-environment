# MVCC 多版本链管理与垃圾回收器

TypeScript 实现的可重复读 MVCC 版本链 + 基于全局最小活跃 ReadView 的可见性修剪器（GC）。
零依赖，使用 Node.js 内置测试运行器（`node:test`）验证。

## 设计

- **`Version<T>`**：版本节点，单向链表按更新时间**倒序**（新 → 旧）链接，`head` 为最新版本。
- **`VersionChain<T>`**：单 key 的版本链。
  - `put(t, value)`：按倒序插入，时间戳必须唯一。
  - `read(t)`：沿链表找到第一个 `version.t <= t` 的版本，即可重复读语义。
  - `prune(minVisibleT)`：保留“对 `minVisibleT` 可见的最新版本”及所有更新版本，
    更旧的版本已被覆盖且对任何当前/未来读者（`t >= minVisibleT`）不可见，安全释放。
- **`ReadViewRegistry`**：追踪活跃只读事务，`minActive` 即全局最小活跃 ReadView。
- **`MvccStore.gc()`**：以 `minActive` 为水位线修剪所有 key 的废弃版本；
  无活跃读者时水位线为 `+∞`，每个 key 仅保留最新版本。
- 内存释放通过 `Version.allocated` 全局存活节点计数验证（GC 时递减并断开引用）。

## 环境要求

- Node.js **>= 22.6**（利用原生 TypeScript type-stripping 直接运行 `.ts`，无需编译；
  推荐 >= 23.6，默认开启）。无需 `npm install`，零依赖。

## 运行

```bash
npm test        # 运行 node:test 内置测试（10 个用例）
npm run demo    # 运行验收场景样例
```

## 验收场景

数据项 `X` 有版本 `v1(t=1), v2(t=5), v3(t=10)`；活跃最小 ReadView 为 `t=8`：

1. `gc()` 修剪 `v1`（`v2` 是对 `t=8` 可见的最新版本，必须保留），`Version.allocated` 从 3 降为 2；
2. `ReadView t=8` 读到 `v2`，`ReadView t=12` 读到 `v3`，重复读结果稳定；
3. 若仍有 `t=3` 的老读者活跃，GC 不会修剪 `v1`；老读者结束后下一轮 GC 才回收。

## 文件结构

- `src/mvcc.ts` — 版本链、ReadView 注册表、MVCC 存储与 GC
- `test/mvcc.test.ts` — `node:test` 测试（含验收场景）
- `examples/demo.ts` — 可运行的验收场景演示
