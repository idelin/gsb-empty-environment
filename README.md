# 多数据源同版本原子刷新与发布

多个数据源刷新到**同一个逻辑版本**后整组一起发布；部分失败时读者继续看到旧的一致视图，
并能拿到失败源清单、对单个数据源重试。不使用任何响应式框架或第三方依赖；
所有状态变更都是事件日志里的一条事件，可随时回放重建；读者永远看不到事务中间态。

运行环境：Node.js ≥ 22.6（直接运行 TypeScript，依赖内置的类型擦除与 `node:test`，零依赖）。

## 运行

```bash
npm test          # node --test tests/**/*.test.ts —— 12 个内置测试
npm start         # node examples/demo.ts        —— 完整运行样例
```

最近一次实际输出保存在：

- 测试结果：`docs/output/test-results.txt`
- 样例运行：`docs/output/demo-run.txt`

## 目录

```
src/events.ts       事件类型 + 纯函数 reducer（唯一的状态推进逻辑，可回放、可校验）
src/store.ts        事件存储抽象与内存实现（可换成文件/数据库）
src/coordinator.ts  RefreshGroup：编排取数、重试、过期隔离与原子发布
tests/              node:test 内置测试（12 个）
examples/demo.ts    可运行样例
```

## 模型

一次刷新称为一个**轮次（round）**，携带调用方指定的**逻辑版本 version**。

事件序列（每个源每次尝试一条）：

```
RefreshStarted
  SourceAttemptStarted → SourceSucceeded | SourceFailed   (每个源可重复：失败→重试)
  RoundPublished          仅当本轮全部源在同一 version 成功，且每轮最多一条
  RoundSuperseded         开启新一轮时，旧的未完成轮次立即过期
```

`RefreshGroup` 自己不保存任何业务状态：事件 append 进 store 后交给纯函数 `reduce` 得到状态。
把同一段事件喂给一个新实例（构造函数会重放 `store.load()`），即可完整恢复视图——
对应"进程重启"测试。

## 需求如何被满足

- **多源刷新到同一逻辑版本、一起发布**：fetcher 返回 `{ value, version }`；
  reducer 在 `RoundPublished` 时校验每个源都 `status=success` 且 `version === 轮次版本`，
  快照一次性整体替换读者视图。
- **读者看不到事务中间态**：读者唯一入口 `getView()` 只返回最近一次 `RoundPublished`
  生成的不可变快照（返回副本，外部改不到内部）。源逐个成功期间视图保持不动。
- **部分失败保持旧视图、报告失败源**：一轮 settle 后只要有失败源就不发布，
  `refreshAll()`/`retrySource()` 的 Promise 解析为 `{ published:false, outcome:"partial",
  failedSources:[...] }`，视图仍是上一个版本。
- **A 新 B 旧不能冒充整组已刷新**：版本不一致的成功结果按失败记录（可重试），
  绝不能进入发布快照。
- **可重试单源**：`retrySource(round, source)` 只重取指定失败源；已成功的源、
  并发重复重试、已发布/已过期轮次都会被拒绝（由事件状态强制，而非仅靠内存标志）。
- **重试成功只发布一次**：发布只有一个入口，且 reducer 对同一轮的第二条
  `RoundPublished` 直接抛错；测试断言日志中发布事件恰好一条。
- **过期结果不混入下一轮**：`refreshAll()` 开启新轮时 append `RoundSuperseded`
  并 abort 旧轮全部在途请求；旧轮任何迟到的成功/失败回调首先检查
  `currentRound !== round`，直接丢弃，既不能发布旧轮，也不能写入新轮。
- **状态操作可回放**：reducer 是纯函数，事件可序列化（不含 Promise/AbortController）；
  `examples/demo.ts` 末尾与两个测试都从空状态重放并逐字段比对。

## 限制与边界

- `MemoryEventStore` 是内存实现；要做真正的崩溃恢复，实现 `EventStore` 接口把事件
  持久化即可。未完成轮次的在途 HTTP 请求本身不会跨进程存活，重启后需要重新发起一轮刷新。
- 逻辑版本号由调用方提供（正整数），reducer 会拒绝非法版本。
