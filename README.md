# reactive-async-derived

不依赖任何响应式框架，手写实现"异步派生值"：状态机 `pending/success/error`、
依赖变化自动取消旧任务、旧结果按代际（generation）拒绝、状态操作可回放、
读者只能看到事务提交后的完整快照。

## 要求

Node.js >= 22.6（推荐 >= 23.6，原生运行 TypeScript，无需编译与任何依赖）。

## 运行

```sh
npm test    # node --test "test/*.test.ts"
npm run demo
```

## 结构

- `src/store.ts` — 事务性状态存储：`transact()` 原子提交一批 Op 并产生不可变快照；
  所有 Op 追加进可 JSON 序列化的日志，`Store.replay()` 可完整重放。
- `src/graph.ts` — 响应式图：`atom()` 可写源、`derived()` 异步派生。
  依赖通过 `get(node)` 动态收集；依赖变化时 `AbortController` 取消旧任务并递增代际，
  晚到的旧结果直接丢弃；`AbortError` 不进入 `error` 状态；节点单例保证共享依赖只执行一次。
- `test/` — Node 内置测试 runner（`node:test`）。
- `examples/demo.ts` — 搜索防抖 + 共享用户数据 + 状态回放的完整演示。
