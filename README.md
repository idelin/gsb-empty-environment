# callback-async-iter

将回调式数据源适配为 `AsyncIterableIterator`，内置有界缓冲（默认 8 条）、反压与恰好一次的清理。

## 用法

```ts
import { fromCallbackSource } from "./src";

const it = fromCallbackSource<number>((handlers) => {
  const timer = setInterval(() => handlers.onData(Date.now()), 100);
  return {
    pause: () => clearInterval(timer),   // 缓冲满时暂停推送
    resume: () => { /* 重新启动推送 */ },
    stop: () => clearInterval(timer),    // 终止时恰好调用一次
  };
}, { bufferSize: 8 }); // 默认值，可省略

for await (const value of it) {
  console.log(value);
  if (value > someLimit) break; // 提前结束：自动触发清理
}
```

## 交付顺序约定

1. 数据按 `onData` 到达顺序 FIFO 交付。
2. 终止信号（`onError` / `onEnd`）不插队：缓冲中已缓存的数据总是先于终止信号交付。
3. `onError` 与 `onEnd` 相邻到达时**先到者胜**：第一个终止通知决定迭代器的终止形态，
   之后到达的终止通知与数据一律忽略。
4. 终止生效（缓冲排空）后，所有挂起的 `next()` 一次性收到终止通知：
   error 终止时全部以同一错误 reject，end 终止时全部 resolve `{ done: true }`。
5. 清理（`controller.stop()`）恰好执行一次——无论正常结束、出错，还是消费者提前
   `return()` / `break`。

## 反压

缓冲达到 `bufferSize`（默认 8）时调用 `controller.pause()`；消费者取走数据使缓冲
跌破上限时调用 `controller.resume()`。反压是协作式的：数据源应在暂停期间停止推送。

## 构建与测试

```sh
npm run build   # tsc -> build/
npm test        # 构建并运行 test/adapter.test.ts
```
