# reactive-store

可重入的状态观察库(TypeScript,零依赖)。观察器可以在通知回调里安全地再次修改状态。

## 快速开始

```ts
import { Store } from "./src/store.ts";

const store = new Store(0);

const unsubscribe = store.subscribe((state, version) => {
  console.log(state, version);
  if (state < 3) store.set(state + 1); // 回调中再次修改:安全,不递归
});

store.set(1); // 依次打印 1 2 3,版本 1 2 3
unsubscribe();
```

运行测试:`npm test`(Node >= 23.6,原生类型擦除,无需编译)。

## 语义规则

### 版本与通知队列
- 每次提交的修改使版本号 `+1`,通知携带 `(state, version)`。
- 派发是**迭代式循环**而非递归调用:回调中的 `set` 只是把新版本入队,
  由当前循环的后续轮次投递。任意深度的链式修改都不会栈溢出
  (测试覆盖 50,000 层)。
- 每名观察器记录自己已见的版本,只会收到**严格递增**的版本。

### 合并规则
- **折叠**:观察器落后多个版本时,只投递最新的 `(state, version)`,中间版本跳过。
- **batch**:`store.batch(fn)` 内的所有修改合并为**一次**版本递增和一轮通知;
  `set` 后写覆盖先写,`update` 基于最新状态组合。嵌套 batch 只在最外层提交。
- batch 不是事务:`fn` 抛异常时,已应用的修改仍会提交。

### 退订
- `subscribe` 返回幂等的退订函数,调用后立即生效——包括在其他观察器的
  回调中、甚至在自己的回调中退订自己。

### 异常隔离
- 观察器抛出的异常被捕获并路由到 `onObserverError`(默认 `console.error`),
  不影响其他观察器、不中断派发、不重试,版本照常推进。

### 通知期间新增观察器
- 新订阅者从**当前版本**起算,不回放历史版本,也不会收到触发它订阅的那次通知。

### 失控防护
- 观察器无条件修改状态会造成无限循环;派发轮次超过 `maxDispatchRounds`
  (默认 100,000)时抛出 `RangeError`。
