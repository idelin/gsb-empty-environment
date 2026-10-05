# callback-async-iterator

将回调式数据源适配为 `AsyncIterableIterator`，内置有界缓冲与协作式反压。

## 用法

```ts
import { fromCallbackSource, type CallbackSource } from "./src/index.ts";

const source: CallbackSource<number> = {
  start({ onData, onError, onEnd }) {
    // 数据源在 start 后随时回调 onData / onError / onEnd
  },
  pause() { /* 缓冲满，暂停产出 */ },
  resume() { /* 缓冲有余量，恢复产出 */ },
  dispose() { /* 释放底层资源，至多被调用一次 */ },
};

const it = fromCallbackSource(source); // 默认最多缓存 8 条
for await (const value of it) {
  // ...
}
```

## 语义约定

- **缓冲上限**：缓冲区最多缓存 `highWaterMark`（默认 8）条。达到上限时调用
  `source.pause()`，消费降到上限以下时调用 `source.resume()`。反压是协作式
  信号，数据源应在收到 `pause` 后停止推送。
- **交付顺序**：缓冲中的数据总是先于终止信号交付。即使错误已经到达，也先
  把已缓冲的数据按序交给消费者，缓冲排空后才抛出错误 / 返回 done。
- **终止竞争**：错误与结束通知可能相邻到达，采用"先到先赢"——第一个终止
  信号生效，之后到达的终止信号与数据一律忽略。
- **唯一清理**：`source.dispose()` 整个生命周期至多调用一次，触发点为首个
  终止信号、消费者提前 `return()`/`throw()`，三者先到为准。
- **挂起读取**：允许多个 `next()` 同时挂起。每条缓冲数据恰好满足一个挂起
  读取（FIFO）；终止信号广播给所有挂起读取——错误使全部 reject，结束使
  全部 resolve done，不会漏掉任何挂起者。错误终止是粘性的：缓冲排空后，
  后续每次 `next()` 都以同一错误 reject。

## 测试

环境需要 Node.js >= 22.6（依赖 `--experimental-strip-types` 直接运行
TypeScript）：

```sh
npm test
```
