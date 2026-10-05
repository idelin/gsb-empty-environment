# temporal-connectivity-checker

时序图连通性检查工具：边在半开时间区间 `[start, end)` 内生效，
判断两个节点在查询区间 `[start, end)` 内是否**始终**连通；
若不始终连通，返回**首次断开的最小时间片**及**割证据**。

## 原理

1. **切片**：取查询区间端点与所有落在区间内部的边端点，排序去重后切成原子时间片。
   每个时间片内生效边集恒定，因此逐片检查等价于检查区间内每一时刻——
   而不是只测试区间两端。
2. **逐片独立 BFS**：对每个时间片，用该片生效的边从 `source` 做一次 BFS，
   若到不了 `target`，该片即首次断开的最小时间片。
3. **割证据**：失败片内，令 S = 从 `source` 可达的节点集，T = 其余节点（含 `target`）。
   所有跨越 (S, T) 的时序边在该片内均不生效（否则 BFS 已越过割），
   每条跨割边附带失效原因：`expired`（已过期）或 `not-yet-active`（尚未生效）。

## 边界语义

- 边生效区间为半开 `[start, end)`：`t = start` 生效，`t = end` 失效。
- **同时开闭**：一条边在 `t` 关闭、另一条在 `t` 开启，连通性在 `t` 时刻无缝衔接。
- **相邻有效段**：`[0, 5)` 与 `[5, 10)` 拼接为连续覆盖，无空隙。
- **空查询区间**（`start >= end`）：vacuously 连通，`slices` 为空。
- `source === target`：平凡连通。
- 空生效窗的边（`start >= end`）视为永不生效。

## 用法

库：

```ts
import { checkConnectivity } from "./src/temporalConnectivity";

const result = checkConnectivity(
  [{ from: "a", to: "b", start: 0, end: 10 }],
  { source: "a", target: "b", start: 0, end: 10 },
  // { directed: true }  // 可选：按有向边处理，默认无向
);
// result.connected / result.slices / result.firstFailure
```

CLI（需要 [Bun](https://bun.sh)）：

```sh
bun run src/cli.ts input.json   # 或 cat input.json | bun run src/cli.ts
```

输入为 `{ "edges": [...], "query": {...}, "directed": false }`，输出 JSON 结果；
始终连通退出码 0，存在断开退出码 1。

## 测试

```sh
bun test
```
