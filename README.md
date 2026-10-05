# bitemporal-file-history

双时态文件历史查询库：属性变化按**业务生效区间**（valid time）与**录入版本**（knowledge version）保存，查询同时指定两个时点。

## 语义

- **半开区间**：业务生效区间为 `[validFrom, validTo)`，`validTo` 缺省为 `Infinity`（开放区间）。
- **双时点查询**：`query(fileId, validTime, knowledgeTime)` 返回该业务时点、该知悉版本下可见的属性及**具体来源修订**。
- **冲突决胜**：同一时点被多条修订覆盖时，录入版本大者优先；版本相同则修订 ID 大者优先。
- **撤销**：`revoke(revisionId)` 构成新的录入版本，仅对 `knowledgeTime >= 撤销版本` 的查询隐藏该修订——撤销只影响之后的知悉版本，历史知悉版本仍可见。
- **迟到补录**：`validFrom` 可以早于已有数据，录入版本照常递增，旧知悉版本的查询结果不受影响。

## 结构

- `src/indexed-store.ts` — 可更新索引实现：按文件维护 `(knowledgeVersion, revisionId)` 有序序列，录入尾部追加、撤销原地标记；查询先二分截取知悉版本前缀，再按区间与撤销状态过滤决胜。
- `src/scan-store.ts` — 逐条扫描参考实现，作为索引实现的正确性核对基准。
- `test/bitemporal.test.ts` — 针对半开边界、决胜规则、迟到补录、撤销最新修订、同一文件跨多区间的定向用例，以及 400 步随机操作 + 密集双时点查询的索引/扫描交叉核对。

## 用法

```ts
import { IndexedStore } from "./src/index.ts";

const store = new IndexedStore<{ status: string }>();
const r1 = store.record({ fileId: "a.txt", validFrom: 0, validTo: 100, attributes: { status: "draft" } });
store.record({ fileId: "a.txt", validFrom: 0, validTo: 100, attributes: { status: "published" } }); // 修订
store.revoke(r1.revisionId); // 撤销 r1，只影响之后的知悉版本

store.query("a.txt", 50, store.currentVersion);
// => { fileId, attributes: { status: "published" }, revision: { ...来源修订 } }
```

## 测试

需要 Node.js >= 23.6（原生 TypeScript 类型擦除）：

```sh
npm test
```
