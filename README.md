# controlled-value

事件溯源式的受控值状态库：区分**受控属性**（外部下发）与**内部默认值**（本地草稿），
所有状态变化通过事件驱动，可逐一核对。

## 核心模型

| 概念 | 说明 |
| --- | --- |
| `externalValue` | 外部下发的受控值；受控时展示以它为准 |
| `draft` | 最近一次真实用户输入（由 `defaultValue` 演进），外部同步永不覆盖 |
| `inputVersion` | 真实输入版本号，仅随输入 / 撤销变化 |
| `confirmedVersion` | 已获外部确认的版本号，单调不减 |
| `value` | 派生展示值：`controlled ? externalValue : draft` |

## 事件

- `input`：真实用户输入。记录历史、更新草稿、推进版本；受控时不改变展示值。
- `external`：外部受控值同步。只更新外部值，不触碰草稿与版本——自身输入的
  回显（echo）因此不会被误认为新输入，从源头阻断反馈循环。
- `set-controlled`：切换模式。切入受控展示外部值（草稿保留）；切出恢复最近真实输入。
- `confirm`：外部确认，携带版本号。版本落后于当前输入版本的**过期确认被拒绝**，
  不得覆盖更新的输入。
- `undo`：撤销最近一次真实输入（含版本回退）；历史耗尽时拒绝。

## 防反馈循环

1. **reducer 层**：`external` 事件永不推进 `inputVersion`，回显不产生新版本；
2. **store 层**：仅当派生展示值真正变化时才通知订阅者，订阅者可安全地把展示值
   回写为外部值（等值回写以 `no-op` 拒绝，循环自然终止）；
3. **可核对**：完整事件日志记录每个事件的应用 / 拒绝结果及原因。

## 用法

```ts
import { ControlledValueStore } from './src/index.ts';

const store = new ControlledValueStore<string>({ defaultValue: '', value: 'EXT' });
store.subscribe((value) => console.log('display:', value));

store.input('typed');        // 草稿 = 'typed'，展示仍为 'EXT'
store.setControlled(false);  // 展示恢复为 'typed'
store.confirm('typed', 1);   // 版本 1 与当前输入持平，接受
store.undo();                // 回到上一次输入前
```

## 运行测试

需要 Node ≥ 23.6（原生类型擦除，无需编译）：

```sh
npm test
```
