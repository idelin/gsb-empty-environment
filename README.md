# 持久化响应式存储与增量 Hydration 恢复器

将内存 Signal 状态树持久化为本地 JSON 检查点；仅带 `@persist` 注解的 Signal 被序列化。
进程重启后加载快照恢复状态，所有派生 Effect 自动重新对齐，临时状态安全重置。

## 环境要求

- Node.js ≥ 23.6（原生 TypeScript 类型擦除 + 内置 `node:test`，零依赖、零构建）

## 结构

- `src/reactive.ts` — 响应式内核：`Signal` / `Computed`（惰性 + 脏标记）/ `Effect`（同步调度、自动依赖跟踪）
- `src/store.ts` — `ReactiveStore`：`@persist` 注解、`checkpoint()` 生成 JSON 快照、`hydrate()` 增量恢复
- `examples/demo.ts` — 跨进程运行样例（进程 A 写检查点 → 全新进程 B 恢复）
- `test/store.test.ts` + `test-fixture/process-lifecycle.ts` — Node.js 内置测试（含真实跨进程用例）

## 用法

```ts
import { ReactiveStore, persist } from './src/store.ts';

const store = new ReactiveStore();
const user = store.signal('user', { name: 'anonymous' }, persist); // @persist：持久化
const draft = store.signal('draft', '<empty>');                    // 临时状态

const greeting = store.computed(() => `你好, ${user.get().name}!`);
store.effect(() => console.log(greeting.get()));

user.set({ name: 'delin' });
store.checkpoint('checkpoint.json');   // 仅序列化 user

// —— 进程重启后 ——
store.hydrate('checkpoint.json');      // 恢复 user、重置 draft、重触发所有 Effect
```

## 运行

```bash
npm run demo    # 跨进程演示：写检查点 → 重启进程 → Hydration 恢复
npm test        # node --test test/*.test.ts
```

## 运行样例输出（`npm run demo`）

```
[进程 A] 构建状态树并修改状态…
  [effect] greeting="你好, anonymous!" theme=light loginCount=0 draft=<empty> token=<none>
  [effect] greeting="你好, delin!" theme=dark loginCount=42 draft=未保存的草稿 token=sess-abc123
[进程 A] 已生成检查点：
{
  "version": 1,
  "createdAt": "2026-10-02T05:52:51.786Z",
  "state": { "user": { "name": "delin", "loginCount": 42 }, "theme": "dark" }
}
[进程 A] 退出，启动全新进程 B 模拟系统重启…

[进程 B] 全新进程，状态树回到初始值，开始 Hydration…
  [effect] greeting="你好, anonymous!" theme=light loginCount=0 draft=<empty> token=<none>
  [effect] greeting="你好, delin!" theme=dark loginCount=42 draft=<empty> token=<none>
[进程 B] Hydration 报告： {"restored":["user","theme"],"reset":["draft","sessionToken"],"skipped":[]}
[进程 B] 最终状态： {"user":{"name":"delin","loginCount":42},"theme":"dark","draft":"<empty>","sessionToken":"<none>"}
```

检查点中只包含 `user` 与 `theme`（@persist），临时状态 `draft` / `sessionToken` 不进入快照；
进程 B 中持久化状态精确复原，Effect 依赖链（signal → computed → effect）被重新触发，临时状态回到初始值。

## 测试结果（`npm test`，Node.js 内置测试运行器）

```
✔ 检查点仅序列化带 @persist 注解的 Signal
✔ hydrate 精确复原持久化状态并重置临时状态
✔ hydrate 触发派生 Effect 依赖链自动重新对齐
✔ hydrate 后依赖链继续正常响应后续变更
✔ 快照中未注册的键被安全跳过
✔ 版本不兼容的检查点抛出明确错误
✔ 跨进程：进程重启后从检查点恢复
ℹ tests 7  ℹ pass 7  ℹ fail 0
```
