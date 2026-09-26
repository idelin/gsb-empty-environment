# nested-scroll-tx

可重放的嵌套滚动事务引擎（TypeScript，零依赖，Node.js 内置测试器）。

## 运行

需要 Node ≥ 23.6（原生类型擦除，直接运行 `.ts`）：

```sh
nvm use            # .nvmrc -> 23.9.0
npm test           # node --test test/*.test.ts
npm run demo       # node demo/run-sample.ts
```

## 模型

- **精确有理数**（`src/rational.ts`）：BigInt 分子分母，增量与滚动位置全程精确，
  浮点残差不丢失（`0.1+0.2 === 0.3`、`1/3×3 === 1`），反向小增量可精确累计回原点。
- **滚动节点**（`src/scroll-node.ts`）：视口/内容尺寸、可滚轴、越界策略
  （`chain` 传递 / `lock` 边界锁定）、可选轴变换（如横向条带 y→x）。
- **滚动事务**（`src/world.ts`）：一次滚轮派发 = 一条事务。剩余增量沿祖先链逐层传递；
  每层记录 `收到/消费/传递/舍弃`，满足逐轴守恒 `收到 = 消费 + 传递 + 舍弃`；
  越根未消费的量记为逃逸量。轴变换每事务至多应用一次；捕获容器变化时剩余量重定向，
  `visited` 集合保证同一增量不被任何节点重复消费。
- **程序滚动**：`scrollTo` 携带来源令牌，回调内同令牌重入被阻止（防反馈环）；
  监听器也可用 `world.isFeedback(source)` 主动忽略程序滚动。
- **惯性**：衰减增量序列派生为独立事务（`source: {kind:'inertia', of: 原始事务id}`），
  原始增量只投递一次。
- **重放**：事务记录事前快照，`world.replay(txId)` 回到事前状态重放并校验逐层记录一致。
- **夹具**（`src/fixtures.ts`）：节点树矩形、字形度量（前进量/行高）与贪心折行排版
  全部内置，核心计算不借浏览器布局引擎。

## 测试

- `test/rational.test.ts` — 有理数解析/算术/精确性
- `test/nested-scroll.test.ts` — 剩余量传递、轴方向、轴变换、小增量累计
- `test/boundary.test.ts` — 边界位置 × 方向 × 锁定策略 × 轴 全枚举，对照精确有理参考
- `test/programmatic.test.ts` — 来源令牌、重入回调枚举、反馈阻断
- `test/replay-inertia-capture.test.ts` — 重放一致性、惯性独立事务、捕获重定向
