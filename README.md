# 多链钱包授权账本

面向 TypeScript 中继宿主的可导入授权账本。账本按“链 + 签名授权身份 + 当前分支历史”维护额度、业务请求和撤销状态；签名恢复函数由调用方注入。

## 快速开始

```ts
import {
  AuthorizationLedger,
  GENESIS_PARENT,
  MemoryPersistence,
} from "./index.ts";

const ledger = new AuthorizationLedger({
  // 必须同步返回地址；账本会要求它等于 authorization.issuer。
  recoverSigner: (authorization, signature) => {
    return signature === `sig:${authorization.issuer}` ? authorization.issuer : "0x0";
  },
  persistence: new MemoryPersistence(),
});

const authorization = {
  chainId: "1",
  contract: "0xcontract",
  issuer: "0xalice",
  beneficiary: "0xbob",
  totalAmount: 9_007_199_254_740_993n, // 可用 bigint，超过 JS 安全整数也保持精确
  validFrom: 100,
  validTo: 200, // 区间两端都包含
  authorizationId: "invoice-42",
};

ledger.appendBlock({
  chainId: "1",
  hash: "0xblock1",
  parentHash: GENESIS_PARENT,
  number: 100,
  events: [
    {
      kind: "spend",
      authorization,
      signature: "sig:0xalice",
      amount: 300n,
      requestId: "payment-A",
      sequence: 0,
    },
  ],
});

ledger.getRemaining("1", "invoice-42"); // 精确 bigint
ledger.getUsedRequests("1", "invoice-42"); // ["payment-A"]
```

`appendBlock`、`addBlock`、`submitBlock` 等价；也提供 `appendEvents`、`appendEvent`、`getBlock`、`getHistory`、`getEvents`、`getStateAt`。

## 授权与事件

- 授权身份由完整授权字段和签名共同哈希，包含链、合约、签发者、受益人、总额度、有效区块区间和授权 ID。
- 同一身份可多次部分消费，累计消费不得超过总额度。
- `spend` 必须有正数 `amount` 和业务 `requestId`；同一身份在同一分支内不能重复使用请求 ID。
- `revoke` 默认撤销授权剩余可用额度；带 `requestId` 时只撤销该业务请求。
- `invalidate` 默认使授权失效；带 `requestId` 时只失效该业务请求。
- 任意事件可带 `revoke: true`，在该事件应用后撤销授权。
- 事件按区块内 `sequence` 排序。缺省时使用数组下标；重复序号会被拒绝。
- `getRemaining` 返回 `总额度 - 已消费`，`getAvailable` 返回当前真正可消费额度（撤销或失效后为 `0n`）。

## 分支与最终性

- 多头按链保存。已知区块可随时成为候选分支；`switchHead` 只接受已存储区块。
- 切换未最终分支时，账本从根区块重放到目标头，离开分支的消费、失效和撤销会全部消失，新分支事件完整重放。
- 相同签名可在不同未最终分支分别使用同名业务请求，互不阻塞。
- `finalize` / `finalizeBlock` 接受区块哈希或最终性证书。证书祖先路径必须与实际存储历史一致。
- 最终块一旦确定：其祖先路径不可回退，任何不以该最终块为祖先的候选头都会被拒绝；已最终扣款不能被快照或重组改写。
- 最终块的后代仍可继续分叉和重组，但只有包含该最终块的历史可成为头。

```ts
ledger.finalize({
  chainId: "1",
  blockHash: "0xblock2",
  ancestors: ["0xblock1", "0xblock2"],
});
```

## 原子持久化

```ts
interface AtomicPersistence {
  load(): Promise<PersistedLedgerState | null | undefined>;
  commit(state: PersistedLedgerState): Promise<void>;
}
```

`PersistedLedgerState` 在一个对象里同时包含全部区块、当前链头和最终性标记。适配器的 `commit` 必须原子替换整个对象，不能先写历史再写链头或最终性。账本每次 `save()` 只调用一次 `commit()`；恢复时会在重新验证签名后重建所有分支状态。

```ts
await ledger.save();
const restored = await AuthorizationLedger.load({ recoverSigner, persistence });
```

`PausablePersistence` 可在暂停期间缓冲完整提交，`resume()` 按顺序刷盘。账本自身也提供 `pausePersistence()` / `resumePersistence()`。

`MemoryPersistence` 支持 `failAtCommit` 注入断电，用于验证任一提交点失败后，恢复出的历史、链头和最终性标记仍属于同一个旧提交。

## 压缩快照

每个授权叶承诺绑定授权身份、总额度、已消费值、请求集合根、撤销与失效标记。授权集合由深度 32 的稀疏 Merkle 树承诺。

```ts
const snapshot = ledger.createCompressedSnapshot([
  {
    state: {
      authorization,
      signature: "sig:0xalice",
      consumed: 400n,
      requests: [{ requestId: "payment-A", status: "active" }],
    },
  },
]);

ledger.verifyCompressedSnapshot(snapshot);  // 只验证
ledger.submitCompressedSnapshot(snapshot);  // 验证后覆盖当前未最终头状态
ledger.applyCompressedSnapshot(snapshot);   // 同上，显式名称
```

验证会拒绝：缺失任一高度兄弟、同一高度出现两个不同根、旧叶证明不成立、新根未绑定变更路径、基根过期、总额度/消费值/请求集合根被篡改，以及最终链上的状态改写。

## 独立回放

`replayEvents(blocks, recoverSigner, { headHash })` 不依赖账本实例，独立从根区块重放指定分支，并返回头、授权视图、剩余额度和已用请求集合，可用于与中继当前状态做对照。

## 验证

```bash
npm test
```

测试覆盖分叉切换、共同祖先消费、撤销/失效后回滚、最终性冲突、有效区块边界、超过 `Number.MAX_SAFE_INTEGER` 的额度、压缩快照证明、断电原子恢复、多链隔离和独立回放。
