# atomic-multi-source-refresh

多数据源刷新到同一逻辑版本后**原子发布**的协调器。纯 TypeScript，无第三方依赖，
使用 Node.js 内置模块（`node:test`）验证。

## 设计

- **事件溯源**：所有状态变更先追加为不可变事件（`RoundStarted` / `SourceLoaded` /
  `SourceFailed` / `Committed`），状态由 `#apply` 折叠得到；`RefreshCoordinator.replay()`
  可从日志完整回放重建（实时与回放共用同一状态转移函数）。
- **读者隔离**：读者只能通过 `getView()` 拿到深冻结快照；快照仅在 `Committed`
  应用时整体替换（单指针切换），轮内中间态永不可见。
- **版本一致性**：上报版本必须等于本轮目标版本，否则记为该源失败——
  "A 新 B 旧"无法冒充整组已刷新。
- **恰好一次发布**：`Committed` 由 `committed` 标志守卫；重试补齐后只提交一次，
  提交后的迟到上报一律拒绝。
- **轮次围栏**：上报携带轮次 id，过期轮次的迟到结果被拒绝，不会混入新一轮。
- **单源重试**：`retrySource()` 只重抓指定源；`refreshAll()` 部分失败时保持旧
  一致视图并通过 `getFailedSources()` 报告失败源及原因。

## 运行

需要 Node >= 22.6（利用原生 TypeScript 类型擦除，无需编译；仓库含 `.nvmrc`）。

```sh
npm test    # node --test test/store.test.ts
npm run demo
```

## 文件

- `src/store.ts` — 协调器（事件日志 + 原子发布 + 围栏 + 重试）
- `src/demo.ts` — 五个场景的端到端演示
- `test/store.test.ts` — 8 个用例覆盖全部需求
