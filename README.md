# interval-topk

前 K 排序库：排序对象的值无法直接读取，只能由提供者（provider）逐次给出**单调收紧的有理数区间**。

## 核心规则

- **顺序只在被证明时确定**：两个对象仅当区间严格不相交（`a.hi < b.lo`）时才确定先后；区间相接（`a.hi == b.lo`）不算分离。
- **相等只靠精确证书**：提供者返回 `exact` 证书且值相同，才判定为相等（同一前缀组）。
- **绝不按区间中点猜测**：未分离的对象一律留在未决组（pending）中。
- **预算驱动**：每次 `refine` 消耗 1 点预算；预算只花在仍可能影响前 K 名的对象上。预算耗尽或无法继续收紧时，返回已确定前缀 + 未决组。

## 提供者契约

`IntervalProvider.refine(item)` 返回：

| 结果 | 含义 | 校验 |
| --- | --- | --- |
| `interval` | 更紧的新区间 | 必须严格包含于上一区间，否则抛 `ProviderContractError` |
| `exact` | 精确值证书 | 必须落在当前区间内，之后不再查询该对象 |
| `exhausted` | 无法继续收紧 | 对象保留当前区间，排序器停止对其查询 |
| `cancelled` | 对象撤销 | 从排名中移除，记入 `cancelled` |

## 用法

```ts
import { Rat } from './src/rational.ts';
import { TopKSorter } from './src/sorter.ts';

const sorter = new TopKSorter<string>(provider, { budget: 100 });
sorter.add('A', { lo: Rat.of(0), hi: Rat.of(10) });
sorter.add('B', { lo: Rat.of(4), hi: Rat.of(11) });
const result = sorter.run(2);
// result.prefix   — 已确定前缀（等值对象同组）
// result.pending  — 未决组（组内顺序未知，不猜测）
// result.complete — 前 K 名是否完全确定
```

## 文件

- `src/rational.ts` — 基于 `bigint` 的精确有理数
- `src/sorter.ts` — `TopKSorter`、区间组件划分、契约校验
- `src/simulated.ts` — 模拟提供者（围绕真值收紧，可注入证书/耗尽/取消/违约）
- `test/sorter.test.ts` — 重叠、相等、违反单调收紧、取消、预算耗尽等场景

## 测试

```sh
npm test    # node --test test/*.test.ts（需 Node >= 22.6，原生运行 TS）
```
