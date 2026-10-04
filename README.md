# interval-topk

面对"只能逐次提供收紧有理数区间"的值提供者，实现保守的前 K 排序（按值降序）。

## 判定规则（绝不猜测）

- **严格先后**：仅当区间严格不相交（`a.lo > b.hi`）才判定 `a` 排在 `b` 前。
  端点相接（`a.hi == b.lo`）不算不相交——真实值仍可能相等。
- **并列相等**：仅当双方都给出精确证书（`exact`）且值相等。
- 其余情况一律未决。区间中点、宽度只用于调度（挑下一个 refine 谁），
  绝不参与排序判定。

## 提供者契约

```ts
interface IntervalProvider {
  readonly id: string;
  refine(): Refinement; // interval（必须 ⊆ 上一区间）| exact（精确证书）| exhausted
  cancel(): void;
}
```

违反契约抛出 `ProviderContractError`：`invalid-interval`（lo > hi 或首个返回非区间）、
`not-tightening`（未单调收紧）、`exact-outside-interval`（证书越出当前区间）、
`refine-after-exact` / `refine-after-exhausted`。

## 用法

```ts
import { TopKSorter, SimulatedProvider } from './src/index';

const providers = [
  SimulatedProvider.halving('a', 10, { radius: 8, intervalSteps: 3 }),
  SimulatedProvider.halving('b', 20, { radius: 8, intervalSteps: 3 }),
];
const sorter = new TopKSorter(providers, { k: 2, budget: 10 });
const res = sorter.run();
// res.status: complete | budget-exhausted | providers-exhausted | cancelled
// res.prefix: 已确定前缀（有序分组，组内为证书证明的并列项）
// res.pending: 未决组（按"互相无法判定"关系的连通分量）
```

- 每次 `refine()` 花费 1 点预算；首次返回的初始区间不计预算。
- 前 K 一旦确定立即停止，不为未决尾部浪费预算。
- `sorter.cancel()` 取消：立即停跑、取消所有提供者，返回当前已确定的部分结果。
- 所有数值为 `Rational`（BigInt 精确有理数），不经过浮点。

## 运行测试

```sh
bun test
```

覆盖：重叠区间收紧后分离、精确证书并列、端点相接保持未决、预算耗尽返回
前缀+未决组（验证不按中点猜序）、违反单调收紧、证书越界、非法区间、
取消（运行中与运行前）、提供者耗尽、K 边界提前停止。
