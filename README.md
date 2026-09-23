# Replayable State With Branch History

一个零第三方运行时依赖的 TypeScript 事件溯源示例。核心不使用 Redux、RxJS、MobX 等现成响应式框架，只由纯 reducer、提交图和派生计算组成。

## 设计要点

- **纯状态重放**：`dispatch` 先把命令转换为事件，状态只通过 `reduce(state, event)` 得到；`checkout`、`state`、`events`、`log` 只折叠历史事件。
- **副作用只执行一次**：副作用描述和事件一起记录为提交的 `effects`，只有实时 `dispatch` 在提交成功后调用；历史查看、提交日志查看、快照恢复都不会重新发送请求。
- **事务原子可见**：一个命令产生的多个事件先完整 reduce，全部成功后才生成一个 commit；任何事件失败都不会写提交，也不会执行该事务的副作用。
- **完整分支历史**：commit 保存 `parentId` 和所属起点分支。从历史提交创建新分支会形成分叉，新分支不会移动 `main` 的指针。
- **历史后恢复实时写入**：在分离头查看历史时禁止直接写入；可从当前历史提交开分支写入，或 `checkout('main')` 回到实时主线继续写入。
- **派生值按历史重算**：派生函数只接收目标提交对应的不可变状态和事件列表；依赖派生会在同一次计算内解析并缓存，不读取主线的新值。

## 文件结构

- `src/replay-store.ts`：提交图、分支、历史回放、事务提交和派生值引擎。
- `src/types.ts`：公共 TypeScript 类型。
- `src/bank.ts`：银行账户示例领域、纯 reducer、命令处理器和派生值。
- `sample/bank.ts`：实时提交、历史回看、历史分支、回主线续写和快照恢复演示。
- `test/*.test.ts`：Node.js 内置 `node:test` 与 `node:assert/strict` 测试。
- `scripts/run.cjs`：自动选择本机 Node.js 22.6+，用原生 TypeScript 执行能力运行测试/样例。

## 运行要求

Node.js 22.6+。项目本身不安装 npm 依赖；当前机器可用 Homebrew 的 Node 25，也可用 `~/.nvm/versions/node/v23.9.0/bin/node`。

```bash
npm test
npm run sample
```

## 样例输出

```text
live main: { alice: 70, bob: 70 } requests = 5
history c2: { alice: 0, bob: 0 } accounts = 2 transfers = 0 requests = 5
historical-fix: { alice: 25, bob: 0 } requests = 6
main continued: { alice: 75, bob: 70 } requests = 7
restored main: { alice: 75, bob: 70 } average transfer = 30 requests = 0
restored branch: { alice: 25, bob: 0 } total = 25 requests = 0
```

关键结果：

- 从 `c5` 回看 `c2` 后请求数仍为 5，说明查看历史没有重放模拟请求。
- 从 `c2` 开 `historical-fix` 后写入，只改变新分支；回 `main` 后原有 5 个实时提交仍在，并且可以继续追加。
- 用历史快照构造新 store 后，主线和分支都能按事件恢复，但请求数为 0；只有恢复后的新 `dispatch` 才发送第 1 个请求。

## 测试结果

`npm test` 的实际结果：

```text
✔ bank transfer is committed atomically and derived values use live history
✔ checkout only folds state events and recalculates historical derived values
✔ a branch started from history preserves both complete histories
✔ restored history sends no requests and can resume with new operations
✔ invalid transfers do not expose or persist a debit-only intermediate state
✔ effects run once for a live dispatch and never for history replay
✔ a historical branch does not overwrite the live branch history
✔ a failed multi-event transaction leaves no commit, state change, or effect
✔ all events in one dispatch are stored as one atomic commit
ℹ tests 9
ℹ pass 9
ℹ fail 0
```

测试覆盖：

- 历史 checkout、`log()`、事件读取和快照恢复不触发副作用。
- 多事件事务只有一个 commit，失败时没有半成品状态、提交或请求。
- 转账的扣款/放款作为一个原子批次，读者不能只看到扣款中间态。
- 历史派生值使用目标提交当时的事件重算。
- 历史新分支与主线各自保留完整提交链，互不覆盖。
