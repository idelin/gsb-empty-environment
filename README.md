# round-robin-channel

生成器轮转库：多个生成器共享一条输出通道，每轮各取一项，按加入顺序交错输出。

## 特性

- **轮转合并**：每一轮从每个活跃且未暂停的源各取恰好一项；耗尽的源自动退出。
- **消费者背压**：纯拉模式。消费者不调用 `next()`，就不会对任何源发起拉取；
  任意时刻每个源最多只有一个未完成的拉取。
- **暂停 / 恢复**：`pause(id)` 立即生效。若暂停时该源恰有进行中的拉取，拉取结果
  会被缓冲而不是丢弃，`resume(id)` 后在下一轮优先发出，期间不发起新拉取。
  所有源都暂停时，通道挂起等待恢复而不是空转。
- **异常隔离**：某个源抛错只移除该源，错误经 `onSourceError` 上报，其余源继续。
- **提前结束**：消费者 `break` / `return()` / `throw()` 时，通道等待所有未完成
  拉取落定，并调用每个源的 `return()` 清理。

## 用法

```ts
import { roundRobin, RoundRobinChannel } from "./src/round-robin.ts";

async function* gen(prefix: string, n: number) {
  for (let i = 0; i < n; i++) yield `${prefix}${i}`;
}

// 简单合并
for await (const value of roundRobin([gen("a", 2), gen("b", 3)])) {
  // a0, b0, a1, b1, b2
}

// 暂停 / 恢复 + 异常隔离
const channel = new RoundRobinChannel<string>({
  onSourceError: (err, id) => console.warn(`source ${id} failed`, err),
});
channel.add(gen("a", 5));
const bId = channel.add(gen("b", 5));
channel.pause(bId);           // 暂停期间 b 不会被拉取
channel.resume(bId);          // 恢复后继续参与轮转
for await (const value of channel) {
  if (value === "a3") break;  // 提前结束：所有源被 return() 清理
}
```

源可以是 `AsyncIterable`、`Iterable`、`AsyncIterator` 或 `Iterator`。

## 运行测试

需要 Node.js >= 22.6（利用原生 TypeScript 类型擦除直接运行 `.ts`）：

```sh
npm test
```

如已安装 TypeScript，可运行 `npm run typecheck` 做类型检查（非必需）。
