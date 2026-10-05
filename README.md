# 区间资源分配器

为半开区间 `[start, end)` 分配整数资源编号，**保留固定绑定**，并使**资源总数**（实际使用的不同编号个数）最小，输出**逐资源时间表**。

## 语义约定

- **半开区间**：`[0, 2)` 与 `[2, 4)` 端点相接，可复用同一资源。
- **零长区间**：`[5, 5)` 为空集，不与任何区间重叠，可共享任意资源。
- **固定绑定**：`resource` 字段指定的编号原样保留；同一资源上的固定区间两两重叠时，直接判定不可行并返回**全部冲突对**。
- **编号空洞**：固定编号允许存在空洞（如只用 5、9）；新增分配优先填充最小编号的空洞，保证总数最小。
- **可扩展性**：区间图上的预染色扩展不能仅按最大重叠数推断——例如
  `A=[0,2)@0`、`B=[3,5)@1`、`C=[1,4)` 的最大重叠数为 2，但必须使用 3 个资源。
  短实例通过穷举（带回溯的 k-染色搜索，k 自下界递增）求得**可证明的最优**；
  搜索预算耗尽时回退 DSATUR 贪心，结果合法但 `optimal = false`。

## 用法

```ts
import { allocateResources } from "./src/interval-allocator.ts";

const result = allocateResources([
  { id: "job-a", start: 0, end: 2, resource: 7 }, // 固定绑定到资源 7
  { id: "job-b", start: 1, end: 4 },              // 待分配
  { id: "job-c", start: 2, end: 3 },              // 待分配
  { id: "tick", start: 5, end: 5 },               // 零长区间
]);

console.log(result.resourceCount); // 2（资源 0 与 7，空洞 0 被填充）
console.log(result.optimal);       // true（穷举证明最优）
for (const [resource, schedule] of result.schedules) {
  console.log(resource, schedule.map((s) => s.id));
}
```

返回的 `Allocation`：

| 字段 | 含义 |
| --- | --- |
| `feasible` | 固定绑定无冲突时为 `true` |
| `optimal` | 穷举证明最优；预算耗尽走贪心时为 `false` |
| `conflicts` | 固定绑定的全部冲突对 `{ resource, a, b }` |
| `resourceCount` | 实际使用的资源总数 |
| `lowerBound` | 扫描线组合下界（最大重叠 + 固定色约束） |
| `assignment` | `id -> 资源编号` |
| `schedules` | 逐资源时间表，按 `(start, end, id)` 排序 |

可选参数：`{ maxSearchNodes }` 控制穷举节点预算（默认 200 000）。

## 运行测试

```sh
npm test   # node --test test/*.test.ts（Node ≥ 23.6，原生类型擦除）
```

测试包含与无预算暴力参考实现的随机小实例对拍（300 例），验证穷举最优性。
