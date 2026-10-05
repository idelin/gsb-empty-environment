# scoped-event-bus

强类型事件总线（TypeScript，零依赖）。核心语义：**显式释放，不把 GC 是否及时发生作为正确性的前提**。

## 运行

```bash
node --test event-bus.test.ts   # 运行测试（Node >= 22.18 或 >= 23.6，原生类型擦除）
node example.ts                 # 运行页面切换示例
```

## 特性与语义

- **显式 dispose 句柄**：`on()` 返回 `Subscription`（`dispose()` 幂等，可重复调用）。
- **AbortSignal 关联**：`signal` abort 即注销；传入已 abort 的 signal 会得到一个已 dispose 的空句柄，不产生计数、从不触发。
- **emit 快照语义**：每次 emit 冻结当前订阅列表，按注册顺序逐个调用；
  本轮之前（或本轮中）已 dispose 的订阅被跳过；本轮新注册的订阅从下一次 emit 生效。
- **once 防重入**：once 订阅在调用前移除，处理器内同步重入 emit 同一事件不会再次触发。
- **同步重入**：支持嵌套 emit，内层 emit 基于当前活跃列表重新取快照。
- **异常隔离**：监听器异常被单独收集并作为 `emit()` 的返回值（`unknown[]`）返回，不吞掉、不阻断其他监听器。
- **弱引用订阅（可选）**：`{ weak: true }` 以 `WeakRef` 持有 handler；`sweep()` / emit 顺带清扫死亡引用，
  另有 `FinalizationRegistry` 尽力而为自动清扫——但可靠释放始终由 `dispose()` 负责。
- **计数与作用域**：`bus.size` / `bus.listenerCount(event)` 给出活动订阅计数；
  `bus.createScope()` 按 scope 批量注销，scope 关闭后拒绝新订阅（抛错）。

## API 速览

```ts
const bus = new EventBus<{ message: { text: string }; tick: number }>();

const sub = bus.on("message", ({ text }) => { ... });          // 返回 Subscription
bus.once("tick", (n) => { ... });                              // 一次后自动注销
bus.on("tick", fn, { signal: controller.signal });             // AbortSignal 关联
bus.on("tick", fn, { weak: true });                            // 弱引用（自己需持有 fn）
const scope = bus.createScope();
bus.on("tick", fn, { scope });                                 // scope 关联
scope.close();                                                 // 批量注销；此后用该 scope 订阅会抛错

const errors = bus.emit("message", { text: "hi" });            // 返回异常数组
bus.size; bus.listenerCount("tick");                           // 活动订阅计数
bus.sweep();                                                   // 清扫死亡弱引用，返回移除数
```

## 测试结果

20 个用例全部通过（Node v23.9.0）：

```
✔ 按注册顺序逐个调用，emit 返回空错误数组
✔ 无监听器时 emit 返回空数组
✔ 本轮新注册的订阅从下一次 emit 生效（快照语义）
✔ 监听器中互相注销：A 注销排在其后的 B，B 本轮被跳过
✔ 监听器中自我注销，后续轮次不再触发
✔ 同步嵌套 emit（重入）：内层 emit 插队执行，外层继续
✔ once 递归：处理器内重入 emit 同一事件不会再次触发
✔ once 只触发一次，且异常时也已在调用前移除
✔ 异常单独收集返回：不吞掉、不阻断其他监听器
✔ 重复 dispose 幂等：计数只减一次
✔ AbortSignal：abort 后订阅失效，不再触发且计数归零
✔ AbortSignal 已取消：订阅立即得到已 dispose 的空句柄
✔ emit 本轮中 abort 排在后面的订阅：该订阅被跳过
✔ 按 scope 注销：close 批量移除，其余订阅不受影响
✔ scope 关闭后拒绝新订阅
✔ scope.close 幂等，可重复调用
✔ 循环创建销毁 scope 后计数归零（不依赖 GC）   ← 10000 次循环，无强制 GC、不看堆大小
✔ 弱引用订阅：存活目标正常触发，sweep 不误删
✔ 弱引用订阅仍可被 dispose 可靠释放
✔ 组合：scope + once + signal 同时关联

ℹ tests 20  ℹ pass 20  ℹ fail 0
```

## 示例输出（node example.ts）

```
— 挂载 PageA —
active subscriptions: 3
emit (PageA 存活):
  [global]   on message: hello
  [PageA] render message: hello
  [PageA] on tick 1

— 切换页面：销毁 PageA，挂载 PageB —
active subscriptions: 3
emit (PageA 已销毁，不应再收到):
  [global]   on message: after switch
  [PageB] render message: after switch
  [PageB] on tick 2

— 异常收集演示 —
  [PageB] on tick 3
  [PageB]    still runs on tick 3
collected errors: 1 -> listener-1 failed

— 卸载全部页面 —
active subscriptions: 3
```

页面切换后旧组件（PageA）的订阅被 `scope.close()` + `AbortController.abort()` 确定性释放，
emit 时不会再收到事件；订阅计数随挂载/销毁精确增减，全程不依赖 GC。
