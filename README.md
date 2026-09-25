# nested-scroll-tx

嵌套滚动容器的滚轮事件分发引擎：按剩余增量逐层传递、边界锁定、轴方向隔离、
子像素残差累积、可重放的滚动事务台账，以及带来源令牌的程序滚动防反馈。
核心计算不依赖浏览器布局引擎——节点树与内容尺寸由夹具（`fixtures/scene.json`）
的字形度量直接算出。

## 要求

- Node.js >= 23.6（原生 TypeScript 类型擦除 + 内置 `node:test`，零依赖）

## 运行

```sh
npm test        # node --test（33 个测试）
npm run sample  # node examples/sample.ts（场景演示 + 台账打印 + 重放校验）
```

## 设计

- `src/tree.ts` — 场景树。内容尺寸 = 行数 × 行高 / 字符数 × 字宽（夹具字形度量）。
- `src/engine.ts` — 核心引擎：
  - `dispatchWheel(point, delta)` 沿命中链（或捕获链）自内向外传递增量游标；
    每层只消费自己量程内的部分，剩余量交给外层，根节点无处可去的部分记为舍弃。
  - 轴变换（`swap`）在进入该层时恰好应用一次并记入台账，不会重复消费。
  - 每个节点每轴保存吸附残差（`residual`）：小数增量跨事件累积，反向小增量
    先抵消已存残差再推动偏移，浮点残差不丢失。
  - `startInertia` 把惯性尾迹拆成一串共享 `originId` 的独立事务，残差经由节点
    状态在步间延续，同一增量不会被消费两次。
  - `scrollTo` 程序滚动携带来源令牌；回调用同一令牌回环滚动会被识别为反馈并拒绝，
    重入深度有上限（8）。
  - 每个事务每层每轴记录 received / consumed / passed / residualBefore/After，
    满足守恒：`received + residualBefore === consumed + passed + residualAfter`。
  - 全部输入（滚轮、惯性参数、程序滚动、捕获变更）记入日志，`ScrollEngine.replay`
    可在全新引擎上重放并得到逐字节一致的台账。
- `src/rational.ts` — BigInt 有理数，作为测试的精确参考模型。
- `test/helpers/reference.ts` — 用有理数独立复现消费算法；对二进有理（dyadic）
  输入要求浮点引擎与参考**完全相等**，对 0.1 类输入要求守恒误差 < 1e-9。

## 测试覆盖（test/）

- `boundary.test.ts` — 内层到底余量给外层；枚举 top/bottom/left/right 四边界的锁定与透传。
- `axis.test.ts` — 横向条不误消费纵向增量；旋转变换只应用一次、只消费一次。
- `residual.test.ts` — 随机 dyadic 序列对拍有理参考（精确相等）；反向小增量累积可移动；0.1 残差不丢。
- `programmatic.test.ts` — 来源令牌阻断反馈；枚举重入回调场景（同令牌阻断、异令牌一次、深度熔断）。
- `inertia.test.ts` — 惯性链守恒、越界恰好一次、亚像素尾迹累积。
- `capture.test.ts` — 手势中途变更捕获容器不重复消费。
- `replay.test.ts` — 混合会话重放后偏移、残差、台账完全一致。

## 样例输出（节选）

```
== 1. wheel over nested scroller: inner bottoms out, remainder flows outward ==
tx#1 [wheel] source=user
  layer     axis   received  consumed    passed  residual  transform
  inner     y   500.00   400.00   100.00     0.00   -
  outer     y   100.00   100.00     0.00     0.00   -

== 4. programmatic scroll with a source token; the mirror echo is blocked ==
  listener sees scroll on inner (source=panel, dy=-200)
  blocked feedback attempts: 1

== replay check ==
  replayed 13 log entries -> identical ledgers: true
```

## 测试结果

```
ℹ tests 33
ℹ pass 33
ℹ fail 0
```
