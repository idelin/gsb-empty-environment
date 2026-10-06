# command-palette

TypeScript 命令库：用有限布尔上下文表达式决定命令可用性，按用户优先级再按 id 排序，
执行时重新检查上下文版本，并通过事件重放验证行为。

## 核心语义

- **有限布尔上下文表达式**：`when` 条件只支持固定语法——上下文键（真值判断）、
  `true`/`false`、`!`、`&&`、`||`、括号，以及 `key == literal` / `key != literal`
  （右值限字符串/数字/布尔字面量）。优先级 `!` > `==`/`!=` > `&&` > `||`。
  解析失败抛出 `ConditionParseError`（带 `offset` 与源表达式），命令不会进入注册表。
- **排序**：`list()` 返回的可用命令按用户优先级 `priority` 降序，相同再按 `id` 升序。
- **上下文版本**：每次实际变更（值真正改变）使 `context.version` 单调递增；
  `list()` 快照携带当时的 `version`。
- **执行时重校验**：`execute(id, args, listedVersion?)` 总是基于**当前**上下文重新求值。
  `listedVersion` 与当前版本不一致时标记 `revalidated`（快照已过期）——
  **列表显示可用不保证稍后仍可执行**：条件不再满足抛 `CommandUnavailableError`，
  命令已删除抛 `CommandNotFoundError`。
- **焦点**：`focus(part)` 维护 `focus` 字符串键与 `focus.<part>` 布尔键，
  供 `when: "focus.editor && !readonly"` 之类的条件使用。

## 事件与重放

所有变更经 `CommandCenter` 记录到 `EventLog`：

- 输入类（参与重放）：`context.set` / `context.unset` / `focus.changed` /
  `command.registered` / `command.unregistered`
- 观察类（用于断言）：`command.listed` / `command.executed` /
  `command.executeRejected` / `condition.parseError`

`replay(events)` 从零重建状态：重放处理器用空函数代替（可用性与排序只依赖元数据），
重放后的可用命令集合、上下文快照与版本号与原始运行完全一致。
解析失败的注册从未产生 `command.registered` 事件，因此重放天然避开坏命令。

## 运行测试

源码只使用可擦除类型语法（`erasableSyntaxOnly`），Node ≥ 22.6 可直接运行：

```sh
npm test            # node --experimental-strip-types --test 'test/*.test.ts'
npm run typecheck   # 需先 npm install（安装 typescript 与 @types/node）
```

## 目录

- `src/expression.ts` — 表达式词法/语法/求值与 `ConditionParseError`
- `src/context.ts` — 带版本号的上下文键值服务
- `src/registry.ts` — 命令注册、排序列表、执行重校验
- `src/focus.ts` — 焦点管理
- `src/events.ts` — 事件类型与 `EventLog`
- `src/command-center.ts` — 门面，统一记录事件
- `src/replay.ts` — 事件重放
- `test/` — 表达式、注册表语义、事件重放（焦点切换/命令删除/解析错误）测试
