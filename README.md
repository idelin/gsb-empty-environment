浏览器把多个页面的失败事件合并成一份报告，旧页面撤销报告许可后，队列里只删掉它的 URL 仍会泄露它是否访问过。给 TypeScript 网络宿主写可导入的聚合与投递组件。每条原始事件带文档代次、顶层站点、策略来源、允许披露的字段集合与采样承诺。聚合桶可共享压缩字典，但输出的计数、最早时间、状态码集合、桶有无和分批边界都只能由收件端当前有权看的事件决定。策略替换、导航、子域继承、端点组撤销是有因果前缀的事件，允许不同顺序送达；重试只能复用当初获准的投递内容，新撤销一旦可见就禁止继续发布旧负载。提供两个入口：增量接收事件并生成候选报告；在实际投递前按不可变证据快照核验报告的全部字段与分批归属。相同端点 URL 不等于同一策略主体，已知缺失的事件页只能返回待证。用成对测试检查，两个历史只在未经授权事件上不同，收件端得到的完整报告序列必须相同；另覆盖额度已满时隐藏事件先到、撤销和确认交错、端点复用及压缩字典跨租户。

## 实现

零运行时依赖，入口为 `src/index.ts`。

- `ingestEvidence(previousSnapshot, events, options)`：第一个入口。增量追加证据、冻结新的不可变快照，并只按当前授权事件重算候选报告。
- `verifyReportForDelivery({ report, snapshot, recipient, dictionary })`：第二个入口。按不可变快照重算全部字段、证明和分批归属，返回 `verified`、`pending_attestation` 或 `rejected`。
- `ReportingHost`：组合增量接收、核验、`DeliveryOutbox` 重试、确认和撤销可见性。
- `createEvidenceSnapshot` / `verifySnapshotIntegrity`：深冻结证据并计算稳定指纹。
- `createCompressionDictionary` / `encodeReportWithDictionary`：跨桶、跨租户共享的只读图字典；字典本身不保存租户状态。

## 隐私边界

- 计数、最早时间、状态码集合、命名桶的存在以及分批序号都从当前授权投影重新计算；被拒绝、未采样、缺少 `batchBoundary` 披露许可或因果未决的事件不会占用批次额度。
- `deliveryId` 和线上 payload 只覆盖收件端可见报告；完整快照指纹仅保存在宿主内部证明中，避免把“多了一条隐藏证据”编码进收件端标识。
- 端点身份是 `(tenantId, principalId, endpoint.groupId, endpoint.endpointId)`。相同 URL 不会合并策略主体，也不会传播撤销。
- 因果前缀缺失或已知事件页缺失时，核验只返回 `pending_attestation`；补齐证据后才会变为 `verified` 或 `rejected`。
- 重试始终返回首次批准的同一 canonical payload 和哈希。当前快照出现相关新撤销时，发件箱进入 `suppressed`；证据页或因果前缀缺失时进入可恢复的 `held`。

## 常用流程

```ts
import {
  DeliveryOutbox,
  ingestEvidence,
} from './src/index.ts';

let snapshot = null;
let candidates = [];
for (const event of events) {
  const result = ingestEvidence(snapshot, event, {
    defaultMaxContributions: 100,
  });
  snapshot = result.snapshot;
  candidates = result.candidates;
}

const outbox = new DeliveryOutbox();
for (const candidate of candidates) {
  const approval = outbox.approve({ candidate, snapshot });
  if (!approval.ok) continue; // pending_attestation 或 rejected

  const attempt = outbox.beginAttempt(approval.delivery.deliveryId, snapshot);
  if (attempt.ok) await post(attempt.endpoint.url, attempt.payload);
}
```

## 测试

```sh
npm test
```

成对测试会把同一组可见历史与“多一条未经授权事件”的历史比较，断言收件端收到的 wire JSON、payload 字节、报告/批次/投递 ID 和压缩编码完全一致。另有先到隐藏事件与额度边界、撤销/确认四种交错、端点 URL 复用、跨租户字典、因果乱序、策略替换、子域继承、导航代次、缺页待证和采样伪造测试。
