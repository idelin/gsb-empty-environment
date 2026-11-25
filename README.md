# HSTS Policy Library

可导入 TypeScript 网络栈的离线 HSTS 策略库。它把企业预置规则、用户网络分区缓存和后到的 HTTPS 更新作为版本化事件合并，而不是做无历史的最后写入获胜。

## 安装与测试

```bash
npm test
```

Node 22/23 可直接擦除类型运行内置测试：

```bash
node --experimental-strip-types --test
```

入口为 `src/index.ts`，根目录 `index.ts` 也会转出全部 API。

## 核心语义

- DNS 名称保存为叶子到根的标签序列；ASCII 大小写归一化为小写，非 ASCII 标签经 IDNA 转为 Punycode。
- 每条规则绑定签发主体、网络分区（含重装代次）、是否包含子域、单调事件 ID 和绝对到期时间。
- 删除事件必须携带 `observedEventId` / `observedVersion`；删除只作用于该精确版本，不会删除同名父域或其他 issuer、分区、来源的条目。
- 删除不会被合成为用户例外；子域条目删除后，仍匹配的父域 `includeSubDomains` 规则继续提供继承决策。
- 可信时间表示为闭区间并集。查询返回 `always-upgrade`、`never-upgrade` 或 `evidence-insufficient`。
- 来源顺序由宿主通过 `sourcePriority` 显式指定。标记 `locked` / `noExemptions: true` 的企业规则不能被用户 `allow` 例外或低权限删除削弱。
- 域名树按根到叶遍历，任意长的未枚举子域都可直接查询，不需要展开所有后代。
- 支持 HMAC-SHA256 签名的范围摘要、最小受影响子树导出、决定依据、JSON 快照和绑定后缀规则/分区代次的检查点。

## 快速示例

```ts
import { HSTSPolicyLibrary } from './src/index.ts';

const hsts = new HSTSPolicyLibrary({
  sourcePriority: ['enterprise', 'https', 'user'],
});

hsts.add({
  eventId: 1,
  host: 'example.test',
  issuer: 'enterprise-policy',
  partition: { id: 'network-partition', generation: 0 },
  source: 'enterprise',
  includeSubDomains: true,
  expiresAt: 2_000,
  locked: true,
});

hsts.add({
  eventId: 2,
  host: 'child.example.test',
  issuer: 'https://child.example.test',
  partition: 'network-partition',
  source: 'https',
  expiresAt: 1_000,
});

hsts.delete({
  eventId: 3,
  observedEventId: 2,
  host: 'child.example.test',
  partition: 'network-partition',
  source: 'https',
});

const decision = hsts.evaluate('a.very.long.child.example.test', {
  at: 500,
  partition: 'network-partition',
});

decision.shouldUpgrade; // true，来自 eventId=1 的父域继承
decision.winner?.eventId; // 1
decision.basis; // 每个后缀候选、来源、到期时间、匹配方式和删除状态
```

## 时间区间

```ts
const temporal = hsts.evaluateTemporal(
  'child.example.test',
  [[900, 1_100], [1_200, 1_300]], // 闭区间并集
  { partition: 'network-partition' },
);

temporal.verdict;
// 'always-upgrade'：所有可能时间都有效且要求升级
// 'never-upgrade'：所有可能时间都没有有效升级策略
// 'evidence-insufficient'：区间横跨到期或证据状态不同
```

默认在 `at === expiresAt` 时视为已到期；需要“到期时间仍有效”的闭区间策略可设置 `expiryBoundary: 'inclusive'`。

## 分区重装

```ts
import { PartitionRegistry, restoreCheckpoint } from './src/index.ts';

const partitions = new PartitionRegistry();
const oldPartition = partitions.current('cache'); // generation 0
const newPartition = partitions.reinstall('cache'); // generation 1

const checkpoint = hsts.buildCheckpoint({
  host: 'child.example.test',
  partition: oldPartition,
});

// 重装后的同名分区会拒绝旧检查点。
restoreCheckpoint(checkpoint, { partitionRegistry: partitions });
```

## 签名范围摘要

```ts
import { createSignedScopeSummary, verifySignedScopeSummary } from './src/index.ts';

const summary = await createSignedScopeSummary({
  prefix: 'example.test',
  partition: { id: 'p1', generation: 3 },
  events: hsts.events,
  signer: 'offline-device-a',
  throughEventId: 42,
  confirmedVersions: [7],
}, 'shared-device-sync-secret');

await verifySignedScopeSummary(summary, 'shared-device-sync-secret', {
  partition: { id: 'p1', generation: 3 },
  host: 'any.long.example.test',
});
```

摘要只选择指定域前缀和精确分区代次内的事件；其他分区不会进入摘要。已确认删除的版本会成为墓碑，旧设备晚到的同版本 add 会保持隐藏。

## 导出与恢复

```ts
const affected = hsts.exportAffectedSubtrees({
  partition: 'p1',
  at: 500,
  includeDecisions: true,
});

affected.minimalRoots; // 可覆盖受影响集合的最小子树根
affected.entries;      // 每个事件/版本的决定依据

const checkpoint = await hsts.createCheckpoint({
  host: 'child.example.test',
  partition: 'p1',
  secret: 'checkpoint-secret',
});

const restored = await verifyAndRestoreCheckpoint(
  JSON.stringify(checkpoint),
  'checkpoint-secret',
);
```

检查点中的 `suffixRulesDigest` 绑定查询名的全部后缀规则，`partitionGeneration` 绑定网络分区代次；快照被篡改或设备重装导致代次变化时恢复会失败。

## 全事件回放

```ts
const replayed = HSTSPolicyLibrary.replay(events, {
  sourcePriority: ['enterprise', 'https', 'user'],
});
```

`test/hsts.test.ts` 用小域名树对照增量应用和按单调事件 ID 全量回放，覆盖同名条目删除、父子策略到期先后、设备重装后的同名分区重用、观察时间横跨到期，以及乱序事件收敛。
