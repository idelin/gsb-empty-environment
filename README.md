# tx-state-container

事务化、可版本化、可分支的 JSON 状态容器（TypeScript，零依赖）。
面向"调试页多状态分片"场景：一次事务提交产生**一个全局版本**，订阅者
永远看到该版本的**完整状态树**，杜绝"购物车回退了、库存还在新版本"
的混合画面。

## 运行测试

需要 Node ≥ 22.6（原生 TypeScript 类型擦除；23.6+ 默认开启）：

```sh
node --test test/store.test.ts
```

测试产物（操作日志 + 回放结果摘要）写入 `logs/fuzz-seed*.json`。
全部验证均为同步确定性执行，无任何 `setTimeout` 碰运气式并发等待。

## API

```ts
const store = new TransactionalStore(initialState, { capacity, onListenerError });

const tx = store.begin({ base? });   // base 缺省为 head；传旧版本号则从该版开分支
tx.set(path, jsonValue);             // path: (string|number)[]，缺失中间层自动建容器
tx.delete(path);                     // 对象删 key，数组按 index splice
tx.commit();                         // => 新的全局版本号（单调递增）

const unsub = store.subscribe((state, version) => { ... });
store.checkout(version);             // => { version, branch, parent, state }（只读历史）
const snap = store.snapshot(version?); // 钉住版本，节点不可回收
snap.release();                      // 释放（幂等），随后触发回收
store.ledger();                      // 内存账本
```

## 语义保证

- **一提交一版本**：版本号全局单调；空事务也产生版本。
- **每版本最多一次通知**：通知按版本 FIFO 投递，回调收到该版本完整状态树。
- **通知期间重入**：回调里 `begin/commit` 立即生效，但其通知排入队列，
  等当前一轮所有订阅者处理完再投递（见测试 `A@1,B@1,A@2,B@2,...`）。
- **错误隔离**：某个订阅回调抛错记入 `store.listenerErrors` 并转发
  `onListenerError`，不影响其他订阅者。
- **取消订阅**：`unsub()` 立即生效（本轮剩余回调中也会被跳过）。
- **分支**：`checkout(v)` 读旧版；`begin({ base: v })` 从 v 分叉出新 branch，
  既有提交永不被覆盖（DAG：每个提交记录 `parent` 与 `branch`）。
- **JSON 兼容不可变树**：`set` 校验并拒绝 function/undefined/NaN/Infinity/
  Symbol/BigInt/循环引用/非平凡原型；值被深拷贝后 `Object.freeze`，
  调用方之后修改入参不会渗入容器。

## 内存账本

- 计费单位是**实际保留的树节点**：每个 object/array 节点权重 =
  1（自身）+ 标量子槽位数；容器子节点各自独立计重，版本间共享结构只计一次。
- 每个提交记录、每个活动快照各持有一份根引用（引用计数）。
- `capacity` 是软上限：超限后按版本从旧到新驱逐**可回收**提交——
  head、活动快照钉住的版本、待通知版本**不可回收**。
- 不变量：`retained === allocatedTotal - freedTotal`，且等于从
  `retainedRoots()` 可达节点去重后的权重（测试中用独立遍历交叉审计）。

## 独立参考模型与回放

- `src/reference.ts`：`ReferenceModel` 为每个版本保存**完整深拷贝**，
  不共享、不回收，作为 oracle。
- `src/replay.ts`：操作日志是纯数据（`begin/set/delete/commit/snapshot/
  release`），可记录、可重放。每步交叉校验：head 一致、每个未驱逐版本
  与参考历史深相等、账本不变量、通知携带完整状态。
- fuzz 测试用种子化 LCG 生成日志（确定性、可复现），4 个种子各约
  100 次提交、3000+ 次交叉校验，日志与结果落盘 `logs/`。

## 文件

- `src/json.ts` — JSON 值校验 / 深拷贝 / 结构相等
- `src/arena.ts` — 引用计数节点竞技场（账本核心）
- `src/store.ts` — TransactionalStore / Transaction / Snapshot
- `src/reference.ts` — 深拷贝参考模型（oracle）
- `src/replay.ts` — 操作日志回放器 + 确定性日志生成器
- `test/store.test.ts` — node:test 测试（8 个用例）
