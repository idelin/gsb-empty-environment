# 滚动列表增量抽取包

面向 TypeScript 浏览器采集宿主。虚拟列表会回收并复用 DOM 节点，节点 ID 只在
单轮快照内有意义；本包以**可信业务键**作为记录的唯一身份，消费每次滚动后的可见
行快照并产出增量变更。

## 核心不变量

- **身份只认业务键**：`nodeId` 仅用于检测节点换键，不参与记录匹配。
- **同键内容变化 => 修订**：内容指纹（键序无关的稳定哈希）变化时输出 `revised`，
  保留全部版本与渲染代次；指纹不变则不产出修订。
- **节点换键 => 新建**：同一 `nodeId` 改绑新业务键时，新键走 `inserted`，
  旧键记录保留并在 `nodeRebinds` 中留证。
- **缺失 != 删除**：本轮未出现的键仅关闭其出现区间（`hiddenKeys`），记录永不删除。
- **完成双条件**：显式结束标记连续出现，**且**首/末边界键连续两轮不变，才置
  `complete`；标记中断需重新累计。
- **循环滚动不完成**：检测到顺序回绕接缝（`wrap-seam`）或边界历史回访
  （`boundary-revisit`）时持续返回未完成证据。
- **出现区间**：每条记录维护 `appearances`，进入视口开区间、离开关区间，
  回滚后再次出现另开新区间。

## 结构

- `src/types.ts`：公共类型（快照、记录状态、变更、完成/未完成证据）。
- `src/hash.ts`：键序无关的稳定序列化 + FNV-1a 32 位指纹。
- `src/extractor.ts`：`IncrementalExtractor`，增量状态机与完成判定。
- `src/virtual-list.ts`：`VirtualList`，固定节点池 + 回收换绑的虚拟列表模型，
  支持头部插入、回滚滚动与循环模式。
- `test/`：基于 `node:test` 的 23 个用例，零依赖。

## 用法

```ts
import { IncrementalExtractor, VirtualList } from "./index.ts";

const extractor = new IncrementalExtractor();
const list = new VirtualList(items, { nodeCount: 8 });

list.scrollBy(4);
const result = extractor.ingest(list.snapshot());
// result.changes: inserted | revised | reseen
// result.nodeRebinds / hiddenKeys / complete / incompleteEvidence

for (const record of extractor.getRecords()) {
  // record.versions、record.appearances（fromRound/toRound）
}
```

## 测试

```bash
node --experimental-strip-types --test test/virtual-list.test.ts test/extractor.test.ts
```

覆盖：节点回收换键、同键修订与键序无关指纹、头部插入不误判回绕、回滚滚动不漏采、
出现区间开关、完成双条件与标记中断、循环回绕/边界回访未完成证据、非法快照快速失败。
