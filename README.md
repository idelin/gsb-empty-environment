# Reactive Time-Travel Controller

跨组件响应式状态快照与时间旅行控制器（TypeScript，零依赖）。

## 能力

- **响应式原语**：`signal` / `computed`（懒求值、脏标记）/ `effect`（UI 或下游订阅，自动去抖批量重渲染，无中间态毛刺）。
- **不可变快照树**：`TimeTravelController` 在每次变更（或每个 `transaction`）后，对所有已注册 Signal 深拷贝并深冻结，生成不可变快照，组织成树（Undo 后再修改会产生分支）。
- **时间旅行**：`undo(n)` / `redo(n)` / `jumpTo(snapshotId)` 跳转至任意历史快照；恢复后 Computed 自动重算、Effect 恰好触发一次，完成 UI/下游重渲染同步。
- **Redo 分支记忆**：Undo 会记录来路分支，Redo 优先沿原路前进，保证状态无漂移。

## 环境要求

Node.js >= 23.6（原生运行 TypeScript，无需编译）。仓库内 `.nvmrc` 指定 v23.9.0。

## 运行

```bash
nvm use            # 或确保 node >= 23.6
npm test           # Node.js 内置测试 runner (node:test)
npm run demo       # 运行验收场景示例
```

## 示例

```ts
import { signal, computed, effect, TimeTravelController } from "./src/index.ts";

const controller = new TimeTravelController();
const count = controller.register(signal(0));
const double = computed(() => count.get() * 2);
effect(() => console.log("render", count.get(), double.get()));

controller.transaction("step-1", () => count.set(1));
controller.transaction("step-2", () => count.set(2));
controller.transaction("step-3", () => count.set(3));

controller.undo(2);  // 精准回到 step-1: count=1, double=2
controller.redo(1);  // 前进一步: count=2, double=4, 无漂移
```

## API 摘要

| API | 说明 |
| --- | --- |
| `signal(v)` / `computed(fn)` / `effect(fn)` | 响应式原语 |
| `controller.register(sig)` | 将 Signal 纳入快照管理 |
| `controller.transaction(label, fn)` | 事务内多次 set 只产生一个快照、一次重渲染 |
| `controller.undo(n)` / `redo(n)` | 回退 / 前进 n 步，返回是否移动 |
| `controller.jumpTo(id)` | 跳转至任意快照 |
| `controller.currentSnapshot` / `history()` | 快照树检视（`values` 为冻结的只读 Map） |
| `controller.canUndo` / `canRedo` | 边界查询 |

注意：快照只包含注册时及之后纳入管理的 Signal；恢复时快照中不存在的 Signal 保持原值。

## 测试

`test/time-travel.test.ts` 覆盖验收边界（3 次修改 → Undo 2 步 → 校验 Signal/Computed 精准恢复 → Redo 1 步无漂移），以及重渲染同步、快照不可变性、分支历史、任意跳转、事务合并快照，共 6 个用例。
