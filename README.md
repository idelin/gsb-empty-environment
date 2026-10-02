# fine-grained-reactive

细粒度响应式核心（Signal / Computed / Effect），通过**拓扑染色 + 两阶段更新**
解决钻石依赖（diamond dependency）问题：汇合点只计算一次，且绝不读到中间瞬态脏值。

## 钻石依赖问题

```
      A (signal)
     / \
    B   C   (computed)
     \ /
      D     (computed)
```

A 变化同时驱动 B 和 C，B 和 C 又共同驱动 D。朴素实现中 D 会被通知两次：
第一次可能在 B 已更新、C 仍旧值时触发（读到不一致的中间瞬态，即 glitch）。

## 机制

1. **拓扑染色**：每个节点携带拓扑高度 `height`（Signal=0，Computed=1+max(依赖高度)，
   Effect=+∞）。源信号写入后沿订阅边 DFS，把全部下游 Computed 标记为 dirty
   并按高度插入优先队列（首次染色才继续扩散，汇合点只入队一次）。
2. **两阶段更新**：
   - 阶段一（标记）：只做染色，不执行任何用户计算；
   - 阶段二（更新）：按高度从小到大出队重算。依赖的高度恒小于当前节点，
     因此任何节点重算时其依赖必然已结算完毕——D 只计算一次，且读到的
     B、C 均为最新版本。
   - 阶段三（副作用）：图完全一致后才运行 Effect。
3. **版本号校验**：节点仅在某依赖的版本号真实前进时才重算，
   "被染色但值未变"的分支不会引发无谓的下游重算。

另提供 `batch(fn)` 将多次写入合并为一次传播；batch 期间读取会同步拉取刷新，
同样不会返回脏值。

## 运行

需要 Node.js >= 22.6（使用内置 `--experimental-strip-types` 直接运行 TypeScript，
以及内置 `node:test` 测试运行器，无任何第三方依赖）。

```bash
npm test       # 运行测试
npm run example # 运行钻石图演示
```

## 测试结果

```
# tests 8
# pass 8
# fail 0
```

覆盖：钻石图 D 单次计算与一致性快照、Effect 观察无瞬态、连续更新、
batch 合并、batch 期间读取、值未变不传播、同值写入不传播、dispose。

## 文件结构

- `src/reactive.ts` — 响应式核心（Signal / Computed / Effect / batch）
- `test/diamond.test.ts` — Node.js 内置测试（node:test + assert）
- `examples/diamond.ts` — 钻石图运行样例
