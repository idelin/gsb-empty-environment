# dynamic-transitive-closure

有向多重图的**全动态传递闭包**库（TypeScript 实现，零运行时依赖）。支持有向边的
批量增加与删除，维护可达关系差量（delta），并可为任意可达点对输出一条见证路径。

## 语义

- 可达关系：`(u, v)` 在关系中当且仅当存在长度 ≥ 1 的有向路径；节点只有位于
  环上（含自环）才可达自身。
- **删除保持其他路径**：删除一条边只移除"所有支撑路径都被打断"的可达对；
  仍由其他路径支撑的可达对完全保留（见下方 diamond 示例）。
- **重复边**：图是多重图。同一条边插入 k 次，需删除 k 次才真正消失；重数
  不归零时不触发任何闭包更新。
- 批量删除先做整体校验：若待删边不存在（含批内重复删除），抛错且图不变。
- 差量是净额：同一批内先删后加同一对，差量中相互抵消。

## 用法

```ts
import { DynamicTransitiveClosure } from 'dynamic-transitive-closure';

const g = new DynamicTransitiveClosure<string>();

g.applyBatch({ add: [['a', 'b'], ['a', 'c'], ['b', 'd'], ['c', 'd']] });

const delta = g.applyBatch({ remove: [['a', 'b']] });
// delta.removed === [['a', 'b']]   —— 只有直连对被移除
// a -> d 仍然成立，由 a -> c -> d 支撑

g.isReachable('a', 'd');      // true
g.witnessPath('a', 'd');      // ['a', 'c', 'd']
g.snapshot();                 // 当前全部可达对
```

### API

| 成员 | 说明 |
| --- | --- |
| `applyBatch({ add?, remove? })` | 批量增删边，返回 `ReachDelta<T>` |
| `addEdge(a, b)` / `removeEdge(a, b)` | 单边便捷接口，同样返回差量 |
| `isReachable(u, v)` | 是否存在长度 ≥ 1 的路径 |
| `witnessPath(u, v)` | 一条具体见证路径，不可达时返回 `null` |
| `reachableFrom(u)` / `snapshot()` | 单源可达集 / 全量可达对 |
| `hasEdge` / `edgeMultiplicity` / `edges` | 边查询（含重数） |
| `nodeCount` / `edgeCount` / `pairCount` | 规模计数 |
| `clear()` | 重置 |

`ReachDelta<T> = { added: [T,T][], removed: [T,T][] }`，满足
`旧关系 − removed + added === 新关系`。

## 算法

- **插入**：新边 `(a, b)` 贡献笛卡尔积 `R⁻(a) × R⁺(b)`（`R⁻`/`R⁺` 为当前闭包中
  a 的反向可达集与 b 的正向可达集），单步即完备，无需级联。
- **删除**：删除只会缩小可达关系，且丢失的对 `(u, v)` 必满足 u 在旧闭包中可达
  某条被删边的起点。因此只对"受影响源点集合"在新图上做 BFS 重算对应行，
  未受影响的行（及其见证路径）保持有效——环上互相支撑的计数问题由此规避。
- **见证路径**：每个点对 `(u, v)` 记录前驱 `via[u][v]`（路径上 v 的前一个节点），
  插入传播与 BFS 重算都会维护；查询时沿前驱链回溯即可。
- 复杂度：插入 `O(|R⁻(a)| · |R⁺(b)|)`；删除 `O(受影响源点数 × (V + E))`；
  空间 `O(V² + E)`。

## 正确性验证

`npm test` 先构建再运行测试：

- 单元测试覆盖链、菱形（删除后其他路径保留）、重复边、自环、环的建立与
  断裂、环支撑对的坍缩、批内增删抵消、非法删除的原子性等。
- 差分测试：30 个随机种子 × 60 轮随机批量增删（含自环、重复边、大量环），
  每轮与"全量 BFS 重算"对照：可达关系完全一致、差量精确重构新旧关系、
  每个可达对的见证路径在当前图中逐边有效、不可达对返回 `null`。

## 构建

源码只使用可擦除的 TypeScript 语法子集。两种构建方式等价：

```sh
npm run build        # 零依赖离线构建（scripts/build.mjs 擦除类型）
npm run build:tsc    # 使用 typescript 包（需要联网安装 devDependency）
```
