# Two-Phase Tree Renderer (TypeScript)

小型树渲染器：渲染分为**准备 (prepare)** 与**提交 (commit)** 两阶段。

## 设计

- `src/host.ts` — 虚拟宿主。独立计数“可见树上的变更”(`visibleMutations`)，
  用于证明准备阶段从不触碰可见树。
- `src/resources.ts` — `Subscription` / `ResourceTracker`，跟踪每个订阅的
  存活与释放次数（双重释放会被记录）。
- `src/renderer.ts` — 核心：
  - 准备阶段逐节点增量构建**脱离可见树的**子树，可暂停；
  - 更新版本到达时，旧准备立即中止并清理其已产生的订阅；
  - 只有最新的**完整**版本才允许提交，提交是单次原子交换；
  - `unmount()` 幂等，清理恰好一次。
- `src/stepper.ts` — 显式步进器，可重放 schedule/step/drain/unmount
  交错脚本。
- `test/replay.test.ts` — 7 个场景：基本提交、准备期零可见变更、
  中途被取代、全交错重放（在 v1 准备的每个暂停点插入 v2）、
  连续提交替换、过期更新忽略、卸载恰好一次。

## 运行

需要 Node >= 22.18（原生类型擦除）：

```sh
node test/replay.test.ts
```
