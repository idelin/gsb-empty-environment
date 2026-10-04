# adjustable-context

可取消上下文库：支持嵌套期限、运行时可调整期限与唯一取消原因。TypeScript 实现，零依赖。

## 语义

- **嵌套期限**：子上下文的有效期限 = `min(自身期限, 父级有效期限)`，永远不会越过父级。
- **可调整期限**：`setDeadline(at)` 随时调整。缩短会同步传播到所有后代（若新期限已过则立即取消）；延长自动被父级期限钳制。
- **唯一取消原因**：首次取消的原因对象被冻结，沿取消树共享同一引用；后续 `cancel` / `setDeadline` 调用无效——已取消的上下文不能恢复。
- **脱离**：`detach()` 返回一个快照当前有效期限、但不再接收父级取消与期限变更的新上下文。
- **回调登记**：`onCancel(cb)` 在已取消（含登记期间到期、取消回调内重入登记）时同步立即触发。

## 用法

```ts
import { Context } from './src/context.ts';

const parent = Context.background().withTimeout(1000);
const child = parent.withDeadline(Date.now() + 5000); // 被钳制到父级期限

child.onCancel((reason) => {
  console.log(reason.code, reason.message); // 'canceled' | 'deadline_exceeded'
});

parent.setDeadline(Date.now() + 200); // 缩短，立即传播给 child
const detached = child.detach();      // 脱离父级，保留当前期限快照
```

## 测试

测试使用 `VirtualClock`（虚拟时钟）驱动，覆盖父级修改传播、子级脱离、回调登记期间到期与唯一原因。需要 Node ≥ 22.6（原生 TypeScript 类型擦除）：

```sh
npm test
```
