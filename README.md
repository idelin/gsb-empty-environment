# coroutine-channel

仿 Go Channel 语义的协程同步原语（TypeScript 实现），内置定容无锁环形队列。

## 组件

- `src/ring-buffer.ts` — 定容环形队列。Node.js 单线程事件循环下，push/shift 天然原子，无需互斥锁即协程安全（无锁语义）。
- `src/channel.ts` — Go 风格 Channel：
  - 带缓冲（`capacity > 0`）与无缓冲（`capacity = 0`）两种模式；
  - 缓冲区满 / 空时自动挂起当前协程（`await` 挂起）；
  - 对端就绪时按 FIFO **精确唤醒**匹配的等待协程（一值只交付一人）；
  - `close()` 广播唤醒所有等待读协程（EOF）；缓冲区剩余数据可先消费完，之后 `recv` 返回 `{ done: true }`；
  - 向已关闭 Channel 发送抛错（对应 Go 的 panic）；
  - 支持 `for await (const v of ch)` 迭代，关闭且耗尽后自动结束。

## 用法

```ts
import { chan } from "./src/channel.ts";

const ch = chan<number>(2);      // 容量 2 的缓冲 Channel；chan<T>() 为无缓冲

await ch.send(1);                // 满时自动挂起
const r = await ch.recv();       // 空时自动挂起；{ value, done }
ch.close();                      // 所有等待读协程收到 { done: true }（EOF）
```

## 运行

需要 Node.js >= 22.6（`--experimental-strip-types`）：

```bash
npm test     # node --experimental-strip-types --test
npm run demo # 运行验收场景样例
```

## 验收场景（tests/channel.test.ts / examples/demo.ts）

1. 协程 P 向容量为 2 的 Channel 写入 3 个数据；
2. 写入第 3 个时缓冲区满，P 自动挂起（`pendingSenders === 1`）；
3. 消费者 C 读取一个数据后，P 立即被唤醒完成第 3 次写入；
4. `close()` 后所有等待读协程收到 EOF。

## 测试结果

```
✔ 验收场景：容量2写3个，P挂起，C读1个后P被精确唤醒，close广播EOF
✔ 无缓冲 Channel：发送方挂起直到接收方就绪（直接交接）
✔ 无缓冲 Channel：接收方先挂起，发送方到达时精确唤醒
✔ FIFO：多个等待接收者按序精确唤醒，一值只交付一人
✔ close 广播：所有等待读协程同时收到 EOF
✔ close 后缓冲区剩余数据仍可消费，之后才 EOF
✔ 向已关闭的 Channel 发送抛错
✔ for await 迭代至通道关闭自动结束
✔ RingBuffer：定容、回绕、满/空边界
ℹ tests 9  ℹ pass 9  ℹ fail 0
```
