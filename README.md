# command-context-registry

TypeScript 命令库：用有限布尔上下文表达式决定命令可用性，状态完全由事件日志驱动，可重放验证。

## 核心语义

- **有限布尔表达式**：`when` 条件只支持标识符、`true`/`false`、`!`、`&&`、`||` 和括号（如 `editorFocus && !readonly`）。无任意函数调用，求值是有限、确定、短路的。引用未设置的上下文键会抛出 `UnknownContextKeyError`，在注册表层包装为 `ContextResolutionError`——不会静默当作 `false`。
- **排序**：`listAvailable()` 返回的可用命令按用户 `priority` 降序、再按 `id` 升序排列。
- **执行重检**：`listAvailable()` 返回 `{ contextVersion }` 快照。`execute(id, { expectedContextVersion })` 在执行前重新比对版本，不一致抛 `StaleContextError`；即使不带版本，也会按**当前**上下文重新求值——列表显示可用不保证稍后仍可执行（条件失效抛 `CommandUnavailableError`，命令被删抛 `CommandNotFoundError`）。
- **事件溯源**：所有状态变更（注册/注销命令、设置/清除上下文键）追加为纯数据事件。`CommandRegistry.replay(events, { handlers, onStep })` 从事件日志确定性重建状态；`onStep` 可逐步断言中间状态。handler 是函数，不进事件日志，重放时通过 `handlers` 重新绑定。

## 用法

```ts
import { CommandRegistry } from './src/index.ts';

const registry = new CommandRegistry();
registry.register({ id: 'editor.save', priority: 10, when: 'editorFocus && !readonly', run: save });
registry.setContext('editorFocus', true);
registry.setContext('readonly', false);

const { contextVersion, commands, errors } = registry.listAvailable();
registry.execute('editor.save', { expectedContextVersion: contextVersion });
```

## 测试

```sh
npm test   # node --test test/*.test.ts（Node >= 23.6 原生运行 TS）
```

`test/replay.test.ts` 用事件重放验证三类场景：焦点切换（可用性随事件逐步翻转）、命令删除（不可列出也不可执行）、条件解析错误（未知键记录为错误、补齐键后恢复），并验证实时操作与重放的快照一致性及重放确定性。
