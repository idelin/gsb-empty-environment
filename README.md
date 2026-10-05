# broadcast-sub

广播订阅库（TypeScript）：每个订阅者拥有固定容量的消息缓冲，溢出时先交付
缺口事件（丢失序号范围），再继续交付最新数据。

## 快速开始

```bash
npm test   # tsc 编译并运行全部测试
```

```ts
import { Broadcast } from './src/broadcast';

const bc = new Broadcast<number>();
const sub = bc.subscribe(
  (event) => {
    if (event.kind === 'gap') {
      console.log(`丢失序号 [${event.lostFrom}, ${event.lostTo}]`);
    } else {
      console.log(event.seq, event.value);
    }
  },
  { capacity: 2 },
);

bc.publish(1);
sub.unsubscribe();
```

## 语义约定

- **序号**：每条消息有全局单调递增序号，从 1 开始。
- **缺口事件**：订阅者缓冲满时丢弃最旧消息；交付前先发
  `{ kind: 'gap', lostFrom, lostTo }`（丢失序号的闭区间），再交付缓冲中的最新数据。
- **本轮新订阅者不接收当前事件**：`publish` 采用快照语义，发布期间调用
  `subscribe` 的监听者从下一次 `publish` 开始接收。
- **退订**：`unsubscribe()` 立即生效——丢弃缓冲中未交付的事件，监听者不再被调用；
  重复调用抛出 `UnsubscribedError`。
- **发布过程可重入**：监听者可在回调中同步退订（自己或他人）、订阅、再次 `publish`。
  发布按"先入队、后交付"两阶段执行，保证每个订阅者始终按序号顺序接收事件。
- **监听者抛错**：不影响其他监听者的交付；`publish` 完成全部交付后，
  以 `AggregateError` 汇总抛出本轮所有监听者异常。

## 文件

- `src/broadcast.ts` — 库实现（`Broadcast` / `Subscription` / 事件类型）
- `test/broadcast.test.ts` — 行为测试（11 个用例）
