# rotation-pool —— 生成器轮转库

多个生成器共享一条输出通道：消费者每调用一次 `next()`，池子按固定轮转
顺序从下一个“活跃”生成器取出一项交付，实现多生成器的公平交错消费。

## 语义

- **轮转调度**：按注册顺序轮转，每轮各取一项；耗尽（done）、失败
  （failed）或被暂停（paused）的生成器自动跳过。
- **消费者背压**：纯拉取模型。没有消费者请求就不会触发生成器；每项交付
  后仅为该生成器做一发有界预取。不变量：**任意时刻每个生成器最多一个
  未完成拉取**，每个生成器的缓冲最多一项。
- **暂停 / 恢复**：`pause(id)` 立即生效——不再向该生成器发起新拉取，
  轮转时跳过。若暂停瞬间恰有在途拉取，允许其完成，结果保留在缓冲中，
  直到 `resume(id)` 后再交付：既不丢失，也不会在暂停期间提前交付。
- **异常隔离**：某个生成器抛错只会让它自身进入 `failed` 状态并触发
  `onError` 回调，其余生成器照常轮转，池子整体不受影响。
- **提前结束**：`close()`（或 `for await` 提前退出触发的 `return()`）
  先等待各在途拉取落定，再调用每个生成器的 `return()`；正在等待的
  消费者收到 `done`。结束后不再产生任何新拉取。

## 用法

```ts
import { RotationPool } from './src/rotation-pool.ts';

const pool = new RotationPool<string>({
  onError: (id, error) => console.warn(`生成器 ${id} 被隔离`, error),
});

pool.add('A', asyncFunctionGen());
pool.add('B', ['b1', 'b2']);          // 同步可迭代对象也可以
pool.add('C', manualAsyncIterator);   // 或直接给 AsyncIterator

pool.pause('C');                      // 暂停空闲生成器
for await (const item of pool) {      // 背压：只按消费速度拉取
  console.log(item);
  if (wantToStop) break;              // 提前结束：自动调用各生成器 return()
}
pool.resume('C');                     // 恢复后继续参与轮转
```

API：`add(id, source)` / `pause(id)` / `resume(id)` / `close()` /
`next()` / `snapshot()` / `closed` / `size`，并实现
`AsyncIterableIterator<T>`。

## 运行测试

需要 Node ≥ 22.18 或 ≥ 23.6（原生 TypeScript 类型擦除）：

```sh
node --test test/rotation-pool.test.ts
```

测试覆盖：轮转顺序与不等长生成器、背压与“最多一个未完成拉取”不变量、
**暂停恰好发生在拉取完成之前**（结果保留到恢复后交付）、暂停空闲生成器、
全部暂停时消费者挂起、异常隔离、提前结束、`for await` 提前退出。
