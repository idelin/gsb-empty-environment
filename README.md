# delivery-window

通知投递库：接收方可并发处理，水位只越过连续完成序号，失败/超时项自动重投。

## 核心语义

- **有限滑动窗口**：在途（已投递未确认）序号全部落在 `(watermark, watermark + windowSize]`；窗口满时 `deliver` 被拒绝（`ignored: 'window-full'`），绝不越界。
- **连续水位**：完成事件可乱序到达，水位只越过连续完成的序号。
- **代次（epoch）防护**：每次投递携带代次，重投时代次递增；只有 `(seq, epoch)` 与当前在途记录精确匹配的确认才生效——旧代次确认无法确认新投递。
- **幂等**：重复完成/重复失败上报无副作用，返回 `ignored` 说明原因。
- **重投**：`fail` 事件立即重投；`timeout` 事件扫描到期项批量重投，均输出在 `redeliveries` 中。

## API

```ts
import { DeliveryWindow } from './src/delivery-window.ts';

const w = new DeliveryWindow({ windowSize: 3, timeoutMs: 100 });

const r1 = w.apply({ type: 'deliver', at: 0 });      // r1.delivered = { seq: 1, epoch: 1 }
const r2 = w.apply({ type: 'timeout', at: 100 });    // r2.redeliveries = [{ seq: 1, epoch: 2 }]
const r3 = w.apply({ type: 'complete', seq: 1, epoch: 1 }); // r3.ignored = 'stale-epoch'
const r4 = w.apply({ type: 'complete', seq: 1, epoch: 2 }); // r4.watermark = 1

w.checkInvariant(); // 校验窗口不变量，越界即抛错
```

事件类型：`deliver` / `complete` / `fail` / `timeout`。
每个 `apply` 返回 `{ watermark, redeliveries, delivered, ignored }`；`run(events)` 可批量应用事件流。

## 测试

```sh
npm test   # node --test（Node >= 23.6 原生运行 TypeScript）
```

覆盖：乱序完成的水位推进、重复完成幂等、旧代次确认拒绝、超时/失败重投、窗口满背压，以及 50×500 步随机事件模糊测试（每步校验窗口不变量）。
