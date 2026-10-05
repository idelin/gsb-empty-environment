# Bitemporal File History Store

文件历史查询库：以**业务生效区间**（valid time）和**录入版本**（knowledge
version）双时间轴保存文件属性变化，查询时同时指定两个时点。

## 语义

- **生效区间半开**：`[validFrom, validTo)`，`validFrom` 含、`validTo` 不含。
- **录入版本**：每次 `record` / `revoke` 提交分配一个单调递增的版本号。
- **冲突决胜**：同一生效时点被多条修订覆盖时，录入版本高者胜；版本相同再按
  修订 ID（`revisionId`）大者胜。
- **撤销**：`revoke` 在新的录入版本上生效，只影响**之后的知悉版本**；查询
  钉在撤销之前的版本仍能看到被撤销的修订。
- **迟到补录**：生效区间在过去、当下才录入的修订是普通插入，在补录版本
  之后的查询中自动覆盖旧知识。
- **查询返回**：属性值 + 具体来源修订（`source`），便于溯源。

## 结构

- `src/types.ts` — 共享类型与决胜/可见性规则
- `src/store.ts` — `BitemporalFileStore`，按文件维护可更新索引
  （按 `(validFrom, validTo, revisionId)` 排序的数组，二分插入/定位）
- `src/reference.ts` — `ReferenceStore`，逐条扫描的参考实现，作为核对基准
- `test/store.test.ts` — 语义单元测试（半开区间、决胜、撤销、补录、跨区间）
- `test/crosscheck.test.ts` — 随机工作负载下索引实现与扫描参考的交叉核对
  （覆盖迟到补录、撤销最新修订、同一文件跨多个区间）

## 运行

Node.js >= 23（原生 TypeScript 类型擦除）：

```sh
npm test
```

## 示例

```ts
import { BitemporalFileStore } from "./src/index.ts";

const store = new BitemporalFileStore();
const r1 = store.record({ fileId: "a", attributes: { status: "open" }, validFrom: 0, validTo: 100 });
const v1 = store.currentVersion;
const r2 = store.record({ fileId: "a", attributes: { status: "closed" }, validFrom: 0, validTo: 100 });

store.query("a", 50, v1)?.attributes.status;   // "open"（旧知悉版本）
store.query("a", 50, store.currentVersion)?.attributes.status; // "closed"

store.revoke(r2.revisionId);                    // 撤销 r2
store.query("a", 50, store.currentVersion)?.source.revisionId; // 回退到 r1
```
