# EventBus

类型安全的事件总线（TypeScript）。核心保证：

- **显式 dispose**：订阅返回 `DisposeHandle`，是唯一可靠的释放途径；可关联 `AbortSignal` 自动释放。正确性不依赖 GC 时机。
- **快照语义**：每次 `emit` 冻结当前订阅列表，按注册顺序调用；本轮已 dispose 的跳过，本轮新注册的下一次 emit 生效。
- **once 防重入**：`once` 订阅在调用前移除，同步重入不会再次触发。
- **同步重入**：监听器内可再次 `emit`，内层作为新一轮重新快照。
- **异常收集**：监听器异常按顺序收集进 `EmitResult.errors` 返回，不吞掉、不阻断其他监听器。
- **弱引用订阅**：`{ weak: true }` 可选，`sweep()` 清扫已死引用（emit 时也会惰性清扫）；仅作内存优化，可靠释放仍靠 `dispose`。
- **scope**：`bus.scope()` 批量管理订阅，`close()` 全部注销且幂等，关闭后拒绝新订阅。`bus.size` / `scope.size` 提供活动订阅计数。

## 运行要求

Node ≥ 23.6（原生运行 TypeScript），或 Node ≥ 22.6 加 `--experimental-strip-types`。

```sh
npm test       # node --test event-bus.test.ts
npm run example
```

## 使用示例

```ts
import { EventBus } from './event-bus.ts';

const bus = new EventBus<{ to: string }>();

// 页面组件在自己的 scope 里订阅，页面切换时整体注销
const page = bus.scope();
page.subscribe(({ to }) => console.log('navigate ->', to));
page.once(() => console.log('first event only'));

bus.emit({ to: '/dashboard' });
page.close();            // 旧组件立即退订，之后不会再收到事件

// AbortSignal 绑定组件生命周期
const ac = new AbortController();
bus.subscribe(handler, { signal: ac.signal });
ac.abort();              // 自动 dispose

// 异常收集
const result = bus.emit({ to: '/x' });
for (const { listener, error } of result.errors) { /* 上报 */ }
```

完整示例见 `example.ts`，运行结果：

```
--- emit #1 (both pages active) ---
[PageA] navigate -> /dashboard
[PageA] first event only
[PageB] navigate -> /dashboard
--- emit #2 (PageA disposed) ---
[PageB] navigate -> /settings
invoked=1 skipped=0 errors=0 active=1
after subscribe with signal, active=2
after abort, active=1
[PageB] navigate -> /error-demo
[survivor] still called for /error-demo
errors collected: 1, first: listener bug
after PageB close, active=2
```

（最后 `active=2` 是示例中未挂 scope 的两个全局监听器：抛异常者和 survivor。）

## 测试

`event-bus.test.ts` 覆盖 12 个场景：注册顺序与快照、嵌套 emit、once 递归、
监听器中互相注销、重复 dispose、AbortSignal 已取消/订阅后取消、异常收集、
scope 关闭语义、循环创建销毁 1000 个 scope 后计数归零（不依赖强制 GC 或堆大小）、
弱引用订阅与 sweep、组合场景。全部通过：

```
ℹ tests 12
ℹ pass 12
ℹ fail 0
```
