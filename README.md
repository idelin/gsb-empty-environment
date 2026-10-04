# bounded-broadcast

广播订阅库（TypeScript）：每个订阅者拥有**固定容量**的消息缓冲；溢出时先交付一个
包含丢失序号范围的 **gap 事件**，再继续交付最新数据。退订后监听器保证不再被调用。

## 快速开始

```ts
import { Broadcast } from './src/broadcast.ts';

const bc = new Broadcast<string>({ capacity: 4 }); // capacity 默认 16

const sub = bc.subscribe((event) => {
  if (event.kind === 'data') {
    console.log(event.seq, event.value);
  } else {
    console.warn(`丢失序号 [${event.from}, ${event.to}]`);
  }
});

bc.publish('hello'); // seq = 1
sub.unsubscribe();   // 之后监听器不会再被调用
```

运行测试（需要 Node ≥ 22.6，利用原生 type-stripping 直接运行 .ts）：

```sh
npm test
```

## 事件模型

每条发布获得一个全局递增序号 `seq`（从 1 开始）。监听器收到的事件为：

| 事件 | 形状 | 含义 |
| ---- | ---- | ---- |
| 数据 | `{ kind: 'data', seq, value }` | 正常消息 |
| 缺口 | `{ kind: 'gap', from, to }` | 序号闭区间 `[from, to]` 的消息因缓冲溢出被丢弃 |

溢出时丢弃最旧的消息；丢失区间自动合并为连续闭区间。gap 事件之后的第一条
`data` 事件满足 `seq === gap.to + 1`。

## 发布过程的语义约定

`publish(value)` 同步执行：先把消息写入**当前所有订阅者**的缓冲区，再逐订阅者
交付。监听器在回调中可以同步退订、订阅或抛错，语义如下：

- **同步退订**：立即生效且幂等。退订后监听器不再被调用——包括本轮发布中
  尚未交付给它的部分（其他订阅者退订它也同样立即生效）。
- **同步订阅**：本轮新订阅的监听器**不会**收到当前正在发布的事件，
  从下一次 `publish` 开始接收（发布时对订阅者做快照）。
- **抛错**：不中断其他订阅者的交付，也不中断该订阅者本轮剩余事件的交付；
  本轮全部交付完成后，`publish` 抛出 `AggregateError` 汇总所有监听器异常。
- **重入发布**（监听器内调用 `publish`）：允许。每个订阅者的事件始终按
  `seq` 顺序交付；这也是缓冲可能溢出、触发 gap 事件的典型场景。

## API

- `new Broadcast<T>({ capacity? })` — `capacity` 为正整数，默认 16。
- `bc.subscribe(listener) => Subscription` — `subscription.unsubscribe()` 退订；
  `subscription.closed` 查询是否已退订。
- `bc.publish(value)` — 同步发布；监听器异常汇总为 `AggregateError` 抛出。
- `bc.subscriberCount` — 当前活跃订阅者数量。
