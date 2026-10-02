# fine-grained-reactive

细粒度响应式核心（`Signal` / `Computed` / `Effect`），通过**拓扑染色 + 两阶段更新**消除钻石依赖（diamond dependency）带来的瞬态脏值（glitch）。

## 问题：钻石依赖

```
    A
   / \
  B   C
   \ /
    D
```

A 变化同时驱动 B 和 C，B、C 又共同驱动 D。朴素实现中 D 会被通知两次：
第一次在 C 尚未更新时执行，读到 `(新B, 旧C)` 的中间瞬态脏值。

## 方案

1. **拓扑染色（level）**：每个节点维护拓扑高度 —— `Signal` 为 0，
   `Computed`/`Effect` 为 `1 + max(依赖 level)`。下游 level 恒大于所有上游。
2. **两阶段更新**：
   - **阶段一（标记）**：源信号写入后，按 level 升序（小顶堆）遍历下游子图，
     仅染色为 `STALE`，不做任何重算；钻石汇合点因 `STALE` 去重只处理一次。
   - **阶段二（结算）**：批量结束后按 level 升序运行 `Effect`；`Computed` 被读取时
     惰性拉取重算，沿依赖边向上递归，保证 B、C 先于 D 更新到最新版本。

结果：每个更新周期内 D **只计算一次**，且读到的 B、C **必为同一版本快照**。

## API

```ts
import { signal, computed, effect, batch } from "./src/reactive.ts";

const a = signal(1);
const b = computed(() => a.value + 1);   // 惰性求值，自动缓存
const c = computed(() => a.value * 2);
const d = computed(() => b.value + c.value);

const stop = effect(() => console.log(d.value)); // 立即执行，依赖变更时重跑

a.value = 2;          // B、C、D 各只重算一次，effect 只触发一次
batch(() => {         // 多次写入合并为一个更新周期
  a.value = 3;
  a.value = 4;
});
stop();               // 销毁 effect
```

## 运行

环境要求：Node.js ≥ 23.6（原生 TypeScript 类型擦除，零依赖、零构建）。
Node 22.6+ 可加 `--experimental-strip-types` 运行。

```bash
npm test        # node --test test/*.test.ts
npm run example # node examples/diamond.ts
```

## 测试结果

```
✔ 钻石图：更新 A，D 只计算一次且读到一致的 B/C
✔ 钻石图 + Effect：每次更新只触发一次，快照始终一致
✔ 钻石图 + batch：多次写入合并为一个更新周期
✔ 拓扑染色：节点 level 严格分层
✔ 更宽钻石：A 驱动 B/C，D 汇合后再被 E 消费
✔ Signal：读写与相同值去重
✔ Computed：惰性求值与缓存
✔ Effect：立即执行、响应变更、dispose 停止
✔ Effect：cleanup 在重跑与 dispose 时调用
✔ 动态依赖：条件分支切换后旧分支不再触发
✔ batch：多个信号写入只结算一次
✔ 链式 Computed：按拓扑序逐层拉取
ℹ tests 12  ℹ pass 12  ℹ fail 0
```

## 结构

- `src/reactive.ts` — 响应式核心（Signal / Computed / Effect / batch）
- `test/diamond.test.ts` — 钻石图验收测试
- `test/reactive.test.ts` — 核心行为测试
- `examples/diamond.ts` — 可运行演示
