# search-state

TypeScript 搜索状态库：区分**查询代际**与**页游标**，安全地组装分页搜索结果。

## 核心语义

- **查询代际（generation）**：每次 `setQuery` 递增代际并清空分页状态。所有页请求携带
  `{ generation, page }` 游标；旧代际的响应（成功或失败）到达时直接丢弃。
- **页游标（page cursor）**：页按页号存储，同代响应可以任意乱序到达，`items`
  始终按页号拼接，且只暴露从第 0 页开始的连续已加载前缀。
- **失败与重试**：`failPage` 标记失败页，`retryPage` 重新武装该页再次请求；
  失败页会阻塞 `nextPageToLoad`，不允许越过缺页继续向后翻页。
- **完成判定**：空页（或不足 `pageSize` 的短页）标记末页；只有当 0..末页
  全部加载、无缺页无失败时 `isComplete` 才为 true。
- **去重**：`items` 按条目 `id` 去重，保留首次出现的位置。

## 结构

- `src/tokenize.ts` — 查询切词：小写化、按非字母数字切分、去重保序。
- `src/store.ts` — `SearchStore`：纯状态层，不发起请求。
- `src/coordinator.ts` — `SearchCoordinator`：用异步 `PageFetcher` 驱动 store，
  支持 `search` / `loadNext` / `retryFailed`。

## 用法

```ts
import { SearchCoordinator } from "./src";

const coordinator = new SearchCoordinator<{ id: string }>(
  async ({ tokens, page }) => fetchPage(tokens, page),
  { pageSize: 20 },
);

coordinator.search("red apples"); // 新代际，请求第 0 页
coordinator.loadNext();           // 请求下一页（可并发）
coordinator.retryFailed();        // 重试所有失败页

coordinator.store.items;          // 按页号拼接、按 id 去重的连续前缀
coordinator.store.isComplete;     // 无缺页且末页已知时才为 true
coordinator.store.missingPages;   // 阻塞完成的页号
```

## 测试

测试通过可控的 deferred fetcher 手动决定响应顺序，覆盖切词、乱序拼接、
旧代际丢弃、失败重试与空末页完成判定。

```sh
bun test          # 运行测试（Bun 原生执行 TypeScript）
npm run typecheck # 需要安装 typescript（npm install）
```
