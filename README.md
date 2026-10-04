# reactive-store

可重入的版本化状态观察库（TypeScript）。观察器可以在通知回调里再次修改状态，
通知按版本排队派发，递归修改不会导致栈溢出。

## 快速开始

```ts
import { Store } from './src/index.ts';

const store = new Store(0);

const unsubscribe = store.subscribe((n) => {
  console.log(n.version, n.state, n.changes);
  // 回调中可以安全地再次修改状态：
  if (n.version < 5) store.set(n.version + 1);
});

store.set(1); // 依次打印版本 1..5，无递归、无栈溢出

// 显式批量：三次修改合并为一次通知
store.batch(() => {
  store.set(10);
  store.set(11);
  store.set(12);
});
```

## 语义约定

### 版本与排队
- 每次 `set` / `update` 提交一个新版本（`version` 严格递增）并进入待派发队列。
- 派发由**迭代式** drain 循环完成：回调中的重入修改只是向队列追加变更，
  由外层循环继续处理，因此任意深度的递归修改都不会栈溢出。

### 批次合并规则
1. 一轮 drain 处理队列中当前积压的全部变更，称为一个**批次**。
2. 同一批次内，每名观察器只收到**一次**通知：
   - `changes` 按版本升序包含该批次中所有它尚未见过的变更；
   - `state` / `version` 为本批次最新值（`state` 取自批次最后一个变更的快照，
     与 `version` 严格一致，不受派发期间重入修改的影响）；
   - `sinceVersion` 为该观察器上一次见到的版本。
3. 回调期间提交的新变更进入**下一批次**，不并入正在派发的批次。
4. 每名观察器所见版本**严格递增**：只投递 `version > sinceVersion` 的变更，
   水位在回调前推进，同一版本绝不重复投递。
5. `batch(fn)` 显式批量：`fn` 内所有提交合并为一个批次，支持嵌套，
   最外层结束时统一派发。

### 订阅生命周期
- **新增观察器**（含通知期间新增的）：从订阅时刻的版本开始观察，
  收不到订阅前提交的变更（含在途批次）。
- **取消订阅**：返回的 `unsubscribe()` 调用后立即生效——即使批次正在
  派发中，被移除的观察器也会被跳过。

### 异常隔离
- 单个观察器抛错不影响其他观察器，通知继续派发。
- 提供 `onError` 选项时，错误逐个上报给 `onError`；
  未提供时，drain 结束后以 `AggregateError` 从顶层修改调用抛出。

## API

- `new Store(initialState, options?)` — `options.onError?: (error, listener) => void`
- `store.getState()` / `store.getVersion()` / `store.getObserverCount()`
- `store.subscribe(listener): () => void` — 返回取消订阅函数
- `store.set(next, action?)` / `store.update(updater, action?)`
- `store.batch(fn)` — 批量修改，合并为一次通知
- 通知对象：`{ state, version, sinceVersion, changes }`，
  其中 `changes: readonly { version, previous, next, action? }[]`

## 运行测试

需要 Node.js ≥ 22.6（使用内置类型剥离直接运行 TypeScript）：

```sh
npm test
```

类型检查（需要安装 TypeScript）：`npm run typecheck`。
