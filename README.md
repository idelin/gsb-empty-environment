# Coroutine Channel（协程通道）

仿 Go Channel 语义的协程同步原语，TypeScript 实现，零依赖，
使用 Node.js 原生 TypeScript 支持（type stripping）与内置测试运行器（`node:test`）。

## 环境要求

Node.js >= 22.18（原生运行 `.ts`，无需编译与任何依赖安装）。

## 组件

- `src/ring-buffer.ts` — 定容无锁环形队列：固定槽位 + head/tail 索引，O(1) 读写；
  单线程事件循环下索引更新天然原子，无需锁。
- `src/channel.ts` — `Channel<T>`：
  - 带缓冲（`capacity > 0`）与无缓冲（`capacity = 0`，rendezvous）两种模式；
  - 通道满时 `send()` 挂起当前协程，通道空时 `recv()` 挂起当前协程；
  - 等待协程进入 FIFO 队列，对端读写时**精确唤醒队首匹配协程**（无惊群）；
  - `close()` 向所有阻塞读协程广播 EOF（`{ value: undefined, ok: false }`），
    阻塞写协程被唤醒并抛 `ChannelClosedError`（对应 Go panic）；
  - 关闭后缓冲区数据仍可读出，排空后才返回 EOF。

## API

```ts
const ch = new Channel<number>(2);
await ch.send(1);                 // 满则挂起
const { value, ok } = await ch.recv(); // 空则挂起；ok === false 即 EOF
ch.close();                       // 广播 EOF
go(async () => { ... });          // 启动协程
```

## 运行

```sh
npm test        # node --test，8 个用例
npm run demo    # 复现验收场景的样例
```

## 验收边界（test/channel.test.ts 首个用例）

1. 协程 P 向容量为 2 的 Channel 写入 3 个数据，第 3 次 `send` 时 P 自动挂起；
2. 消费者 C `recv` 一个数据后，P 被精确唤醒并完成第 3 次写入；
3. `close()` 后，所有等待中的读协程收到 EOF 广播。
