# signal-time-travel

跨组件响应式状态快照与时间旅行控制器（TypeScript，零依赖）。

## 能力

- **响应式核心**：`signal` / `computed` / `effect`，自动依赖追踪、惰性重算、批量事务（`batch`）
- **不可变快照树**：每次状态提交后，对全局所有 Signal 做 `structuredClone` 深拷贝，生成不可变快照节点；Undo 后再修改会产生分支
- **时间旅行**：`undo` / `redo` / `jumpTo(id)` 跳转至任意历史快照；恢复在单事务内完成，自动通知 Computed 失效与 Effect 重渲染（每次跳转只触发一次下游渲染，无中间态）
- **无漂移**：快照存取均深拷贝，外部对当前状态的原地篡改不会污染历史

## 环境要求

- Node.js **≥ 23.6**（原生运行 TypeScript，零构建）；Node 22.6+ 可加 `--experimental-strip-types` 运行
- 仅使用 Node 内置模块（`node:test` / `node:assert`）测试，无任何 npm 依赖

## 运行

```bash
npm test        # node --test test/*.test.ts
npm run demo    # node examples/demo.ts
```

## API 速览

```ts
import { signal, computed, effect, batch, TimeTravelController } from './src/index.ts';

const count = signal(0);
const double = computed(() => count.get() * 2);
effect(() => console.log('render:', count.get(), double.get()));

const tt = new TimeTravelController(); // 自动记录此后每次提交
count.set(1);                          // → 快照 #1
batch(() => { count.set(2); });        // 多次修改合并为一个快照
tt.undo();                             // 回退一步，UI 自动同步
tt.redo();                             // 前进一步
tt.jumpTo(0);                          // 跳转至初始快照
tt.snapshots();                        // 查看整棵快照树
```

## 设计要点

- `src/reactive.ts`：响应式核心。写入进入事务队列，flush 时**先排空所有节点通知（含 Computed 级联失效），再统一执行 Effect**，保证下游不会观察到中间态；每次 flush 结束触发一次 `onCommit`。
- `src/history.ts`：`TimeTravelController` 订阅 `onCommit`，记录 `signalId → structuredClone(value)` 的不可变快照树（`parentId` / `childIds` / `preferredChildId`）。恢复时在单个 `batch` 内写回所有 Signal（同样深拷贝），`restoring` 标志防止恢复过程被误记为新历史；Undo 时记录来路，Redo 优先沿原路前进。

## 测试结果（Node v23.9.0）

```
✔ 验收：3次修改 → Undo×2 精准恢复 → Redo×1 无漂移
✔ jumpTo 跳转至任意历史快照
✔ Undo 后产生新修改会创建分支，Redo 仍沿原路
✔ 快照不可变：后续对对象值的篡改不影响历史
✔ batch 合并多次修改为单个快照；等值 set 不产生快照
✔ computed 惰性重算且缓存：Undo 后读取触发一次重算
ℹ tests 6  ℹ pass 6  ℹ fail 0
```

验收路径：3 次修改（`count=1` → `count=2` → `step=5`）→ `undo()×2` 后 `count/step/double/total` 与渲染日志均精准回到第 1 步（`1/1/2/2`）→ `redo()×1` 后恢复到第 2 步（`2/1/4/3`），且每次 Undo/Redo 仅触发一次重渲染，无漂移。
