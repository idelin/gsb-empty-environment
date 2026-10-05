# 事务化状态容器（tx-state-store）

面向“调试页面多状态分片”的场景：回退历史时，购物车、库存等所有分片必须落在
同一个全局版本上，杜绝“购物车回去了、库存还在新版本”的混合画面。

纯 TypeScript、零依赖；Node ≥ 22.6 直接以类型擦除方式运行（本仓库用 v23.9 验证）。

## API

```ts
import { Store } from "./src/store.ts";

const store = new Store(
  { cart: { items: [] }, inventory: {} },  // 初始状态（根必须是对象：分片 -> 子树）
  { capacity: 1024 },                       // 容量上限：实际保留的树节点数（对象/数组）
);

const off = store.subscribe((state, version) => { /* 每版本最多一次，state 为完整状态 */ });

const tx = store.begin();                   // 或 store.begin({ from: 旧版本号 }) 开分支
tx.set(["cart", "items", 0, "qty"], 3);     // 值必须是 JSON 兼容树，会被深克隆+冻结
tx.set(["inventory", "a"], 2);              // 多分片写入在同一事务中原子生效
tx.delete(["cart", "coupon"]);
tx.commit();                                // 产生一个新的全局版本并触发通知
// tx.abort();                              // 中止：不产生版本、不通知、不占账本

const snap = store.snapshot();              // 固定当前头部版本
const old = store.checkout(2);              // 读取历史版本（已回收则抛 VersionUnavailableError）
const branch = old.begin();                 // 从旧版开分支；提交得到全新版本号，不改写历史
old.release();                              // 释放固定；之后该版本可被容量回收

store.version;                              // 头部版本号
store.state;                                // 头部完整状态（冻结）
store.ledger;                               // { retainedNodes, versions } 内存账本
```

## 语义保证

- **一提交一版本**：版本号全局单调递增；分支提交只追加新版本，既有历史永不覆盖。
- **通知轮次制**：订阅者每版本最多收到一次通知，且看到该版本的完整状态。回调期间
  发起的新事务进入队列，排到本轮所有订阅者之后；回调抛错被收集到
  `store.listenerErrors`，不影响其他订阅者。回调中取消订阅立即生效（本轮也不再调用）。
- **不可变 JSON 树**：只允许 null/boolean/有限 number/string/数组/普通对象；
  拒绝 undefined、NaN/Infinity、function/symbol/bigint、循环引用、数组空洞。
  所有节点深冻结；`set` 的值深克隆脱钩；`__proto__` 键被拒绝/安全处理。
- **容量按保留节点计费**：持久化结构共享，未修改的子树跨版本复用同一批节点；
  引用计数记账，`ledger.retainedNodes` 只计实际保留的对象/数组节点。超上限时从
  最老版本开始逐出“未固定且非头部”的历史；活动快照/检出句柄固定的版本不可回收；
  若固定节点本身就超上限，提交事务性失败（`CapacityExceededError`），不留痕迹。

## 测试与回放

```bash
npm test          # node --test：12 个用例
npm run replay    # 打印操作日志回放事件流（已保存到 replay-result.txt）
```

测试要点（无任何 setTimeout 碰运气，全部同步、确定性）：

- **独立参考模型**（`test/harness.ts` 的 `ReferenceModel`）：完整深拷贝历史，
  无任何结构共享；每个提交版本、每次通知、每次检出都与之逐版本 `deepEqual`。
- **操作日志回放**（`test/oplog.ts` + `test/replay.test.ts`）：覆盖互相依赖的
  双分片、通知中重入提交、回调抛错、回调中取消订阅、分支提交、快照固定/释放、
  容量回收的纯数据日志；在两个 Store 实例上回放，断言事件流逐字节一致。
- **随机对照**：种子化 PRNG 生成 300 个事务（无限容量 + 有限容量两组），
  逐步对照参考模型，并验证账本守恒（`retainedNodes` 等于从保留版本可达的节点数、
  不超上限）。
- **手工核对账本**：容量用例中的每个 retainedNodes 数值（5/9/11/9/9 与回放日志
  的 10）均由人工按结构共享规则推算，与实现独立。

## 最近一次结果

```
✔ tests 12  ℹ pass 12  ℹ fail 0
```

回放终态：`head=v6`，`ledger: retainedNodes=10 versions=[5,6]`，
`listenerErrors=5`（订阅者 B 每轮抛错，均被收集、未影响他人）。
