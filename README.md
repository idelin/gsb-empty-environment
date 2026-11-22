# Broadcast Routing for Browser Hosts

一个无外部依赖的 TypeScript 广播路由包。它把“文档实例是否获授权”和“消息在哪个分区可见”分开建模，可用于预渲染页面、bfcache 恢复、跨进程广播和可重放验收。

```ts
import {
  BroadcastRouter,
  makePartition,
  verifyPartialOrder,
} from "./src/index.ts";
```

包根导出位于 `src/index.ts`，`package.json` 已配置 `main`、`types` 和 `exports`。源码可由 Node 23 的 type stripping 直接运行，也可由浏览器宿主打包器导入。

## 核心模型

- `PartitionIdentity` 严格绑定四项：顶层注册域、发起来源、跨站祖先位、配置版本。四项均相同才属于同一消息分区。
- `DocumentSnapshot` 同时带 `instanceId` 和全局递增、不可复用的 `lifecycleId`。导航会创建新生命周期；恢复不会把旧生命周期编号交给新文档。
- 页面状态为 `prerender | active | bfcache | closed`。预渲染页和 bfcache 中的页面不满足“收到时已获授权”，不会进入投递队列。
- 频道会话有自己的 `generation`。同一文档恢复后重开同名频道会得到新代次，旧代次的迟到包不会与新频道混淆。

## 快速开始

```ts
const partition = makePartition({
  topLevelRegistrableDomain: "example.com",
  initiatorOrigin: "https://app.example.com",
  crossSiteAncestor: false,
  configVersion: "2026-11",
});

const router = new BroadcastRouter();
router.createDocument({
  instanceId: "sender",
  displayUrl: "https://app.example.com/",
  partition,
  state: "active",
});
router.createDocument({
  instanceId: "receiver",
  displayUrl: "https://app.example.com/room",
  partition,
  ancestorChain: ["https://app.example.com", "https://app.example.com"],
  state: "active",
});

const received: unknown[] = [];
router.subscribe("receiver", "room", {
  id: "room-sub",
  visibleOrigins: ["https://app.example.com"],
  onMessage: (delivery) => received.push(delivery.payload),
});

router.send("sender", "room", "hello");
// received === ["hello"]
```

### 生命周期与导航

```ts
router.enterBackForwardCache("receiver");
router.restoreDocument("receiver");   // 同一文档生命周期恢复，订阅重新武装

const next = router.navigateDocument("receiver", {
  instanceId: "receiver-next",
  keepDisplayUrl: true,
  keepChannelNames: true,
});
// next.displayUrl 和频道名可以保留；next.subscriptionIds 必须为空
```

进入 bfcache、恢复、关闭、导航都会推进文档阶段并丢弃旧阶段尚未回调的队列。新阶段只接收新阶段内到达的消息。

### 可暂停回调

```ts
router.pauseReceiving("receiver", "room-sub");
router.send("sender", "room", "queued");

await router.resumeReceiving("receiver", "room-sub");
// 仅在文档实例、生命周期阶段、订阅、分区和可见范围仍有效时回调
```

`onMessage` 也可以在自身执行期间调用 `pauseReceiving`。回调支持同步或返回 `Promise`，同一订阅严格串行。调用 `unsubscribe` 会清空并关闭该订阅的待处理投递；之后的恢复不会把它们交给新页面。

## 投递与分区规则

每条投递必须同时满足：

1. 消息的 `sendSnapshot` 与接收文档当前分区相同。
2. 传输层在收到消息时捕获的 `deliverySnapshot.partition` 与接收文档当前分区相同。
3. 订阅创建时冻结的分区快照仍与文档分区相同。
4. 订阅的 `visibleOrigins` / `visiblePartitions` 允许发送者。
5. 接收文档在消息到达时已经是 `active`，且订阅尚未撤销。

`transmit(envelope, { deliverySnapshot })` 可以注入传输层捕获的投递分区。未显式提供时，每个候选接收方在到达时的当前分区就是其投递快照。

两个 URL 相同的页面只有在文档实例、生命周期和分区都匹配时才可能成为同一消息的接收方；`ancestorChain` 保留在文档审计信息中，跨站祖先位参与分区身份。

## 线路传输与去重

`send()` 默认立即在当前进程内投递。设置 `deliver: false` 可取得信封并稍后注入：

```ts
const first = router.send("sender", "room", "payload", { deliver: false });
router.transmit(first.envelope);
router.transmit(first.envelope); // 至少一次传输的重复副本
// 第二次返回 { status: "duplicate", deliveries: [] }
```

去重键由 `dedupeKeyFor(senderInstanceId, channelGeneration, sequence)` 生成。接收端同时维护：

- 已接受消息键集合，保证重复副本不产生新投递。
- 每个发送实例/频道代次的序号水位，拒绝迟到的旧序号。
- 每个发送实例/频道名的代次水位，拒绝同名频道的旧代次。
- 若发送实例仍在当前进程，还会校验 `lifecycleId`、频道会话和阶段是否仍有效。

## 确认与关闭

每个 `deliveryId` 最多保留一个终态决定：

```ts
const decision = router.closeDelivery(deliveryId, "close-ack-lost");
router.confirmDelivery(deliveryId, "late-ack"); // 返回原决定，不覆盖

const audit = router.getDecisionAudit(deliveryId);
// audit.decision: closed；audit.attempts 记录首次成功与后续被拒尝试
```

确认和关闭谁先到达，谁产生唯一有效决定；重试可以追加审计尝试，但不会改写 `decision.at`、`status` 或 `by`。

## 偏序验收器

事件数组本身不提供顺序，只有 `dependsOn`、`after`、`predecessors`、`causalPredecessors` 和 `before` 形成偏序。验收器枚举全部合法拓扑序，对每个顺序运行独立路由，并按投递指纹求交：

```ts
const report = await verifyPartialOrder([
  {
    id: "make-sender",
    action: {
      type: "createDocument",
      document: "sender",
      displayUrl: "https://app.example.com/sender",
      partition,
      state: "active",
    },
  },
  {
    id: "make-page",
    action: {
      type: "createDocument",
      document: "page",
      displayUrl: "https://app.example.com/",
      partition,
      state: "active",
    },
  },
  {
    id: "subscribe",
    dependsOn: ["make-page"],
    action: {
      type: "subscribe",
      subscription: "sub",
      document: "page",
      channel: "room",
    },
  },
  {
    id: "send",
    dependsOn: ["subscribe", "make-sender"],
    action: {
      type: "send",
      messageId: "m1",
      document: "sender",
      channel: "room",
      payload: "hello",
    },
  },
]);

report.always;         // 所有合法顺序都会发生的投递
report.orderDependent; // 只在部分并发顺序发生的投递
report.orderCount;
report.orders;         // 每个拓扑序及实际投递键
```

`maxOrders` 默认 2000；超过上限会抛出 `PartialOrderTooLargeError`，要求增加依赖约束或提高上限。循环依赖会被拒绝。

## 验证

```bash
npm test
```

测试覆盖：

- 相同 URL、不同祖先链/跨站祖先位的页面隔离。
- 预渲染页激活前不能接收先前消息。
- 导航保留 URL/频道名但不继承订阅。
- 暂停回调、回调内自暂停、恢复顺序和撤销。
- bfcache 恢复后同名频道的新代次与旧包拒绝。
- 旧发送者序号迟到与至少一次重复副本。
- 关闭确认丢失、确认/关闭竞争及单一决定审计。
- 偏序验收器的“必然投递”和“并发相关投递”。
