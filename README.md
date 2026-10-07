# controllable-state

事件溯源的受控/非受控状态库（TypeScript）。核心思想：**命令事件驱动状态迁移，每次迁移落一条日志事实**，展示值、草稿、版本全部可由日志核对。

## 核心概念

- **受控属性 vs 内部默认值**：传入 `value` 即受控（展示以外部值为准）；否则以 `defaultValue` 为内部草稿初始值。两种模式可运行时切换。
- **草稿保留**：`draft` 始终保存最近真实输入。受控模式下用户输入不改变展示值，但草稿不丢；切回非受控时立即恢复展示。
- **版本化确认**：每次真实输入（含撤销）使 `version` 单调递增。`confirm(version)` 仅在 `version >= 当前输入版本` 时被接受；**旧确认被拒绝并记入日志，不能覆盖新输入**。
- **反馈循环防护**：
  - `onChange` 仅由用户来源事件（`input` / `undo`）触发，外部同步事件（`external` / `confirm` / `setControlled`）绝不触发；
  - 父级回显相同值被抑制（`external-echo-suppressed`）；
  - 订阅者仅在展示值实际变化时被通知；
  - `onChange` 回调中同步派发的事件排队，当前迁移完成后按序应用，日志顺序确定。

## 事件模型

命令事件（`StoreEvent<T>`）：`input` / `external` / `setControlled` / `confirm` / `undo`。

日志事实（`JournalRecord<T>`）：`input-accepted` / `external-applied` / `external-echo-suppressed` / `external-ignored-uncontrolled` / `mode-changed` / `confirmed` / `confirm-rejected` / `undo-applied` / `undo-noop`。

每次 `dispatch` 恰好产生一条日志；重入事件返回 `event-queued` 并在当前迁移后补记真实日志。

## 用法

```ts
import { createControllableStore } from './src/index';

const store = createControllableStore<string>({
  value: 'controlled',        // 省略则非受控
  defaultValue: 'initial',
  onChange: (value, version) => { /* 仅用户输入/撤销触发 */ },
});

store.input('typed');         // 用户输入：草稿更新，version+1
store.syncExternal('v2');     // 父级下发受控值
store.setControlled(false);   // 切换模式，草稿保留
store.confirm(1);             // 带版本确认；旧版本返回 confirm-rejected
store.undo();                 // 撤销最近输入

store.getSnapshot();          // { value, draft, controlled, version, confirmedVersion, dirty, ... }
store.getJournal();           // 完整日志，用于核对每次迁移
store.subscribe((value) => { /* 展示值变化时通知 */ });
```

## 测试

```sh
bun test
```

测试按场景核对日志序列：输入、模式切换、旧确认拒绝、撤销、回显抑制与受控回路（`onChange` 同步回写不会无限递归）。
