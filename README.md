# BFCache 冻结与恢复状态机

用户注销后按后退键恢复 BFCache 页面时，旧 DOM、内存令牌、定时器、网络回调和可提交表单都可能继续访问敏感数据。本包给 TypeScript 浏览器宿主提供一个可导入的页面冻结/恢复状态机，以及一个确定性的最短违规轨迹搜索器。

核心原则：**BFCache 恢复不是“继续执行旧页面”，而是一次必须重新通过授权、策略和分区校验的文档恢复。** 校验失败时旧文档立即成为 `discarded`，敏感状态被清理，排队任务被丢弃，并同步创建全新文档。

## 快速使用

```ts
import {
  AccountAuthService,
  createBfcacheHost,
} from "./index.ts";

const auth = await AccountAuthService.create();
const active = await auth.issueActive({
  accountID: "account-1",
  authSessionID: "auth-session-1",
});

const host = createBfcacheHost({
  browserSessionID: "browser-session-1",
  authAuthority: auth,
  policyAuthority: {
    read: () => ({
      generation: 1,
      allowSensitiveDom: true,
      allowNetwork: true,
      allowFormSubmission: true,
      allowMessagePort: true,
    }),
  },
});

const partition = {
  topLevelSite: "https://app.example",
  origin: "https://app.example",
  browserSessionID: "browser-session-1",
};

await host.navigate({
  historyEntryID: "A",
  topLevelSite: partition.topLevelSite,
  origin: partition.origin,
  authSessionID: active.authSessionID,
  sensitiveDom: { secret: "SENSITIVE-VALUE" },
});

host.pagehide({ historyEntryID: "A", persisted: true });

// 注销：epoch 递增并变为 Revoked。
const revoked = await auth.revoke({ authSessionID: "auth-session-1" });
await host.receiveAuthEpoch(revoked);

// 恢复前会重新读取 authority；这里得到 discarded + 全新导航。
const result = await host.pageshow({
  historyEntryID: "A",
  persisted: true,
  currentPartition: partition,
});
// result.outcome === "discarded"
// result.reason === "auth-session-revoked"
```

`pageshow` 和 `receiveAuthEpoch` 会排空 control lane，但不会执行文档任务。需要手工交错事件时，使用 `enqueuePageshow` / `enqueueAuthEpoch` / `enqueuePolicy`，再调用 `runNext` 或 `runUntilIdle`。

## 文档身份与状态

`DocumentIdentity` 绑定：

- `topLevelSite`
- `origin`
- `browserSessionID`
- `documentGeneration`（全局单调递增的 fencing token）
- `authSessionID`、`accountID`、`authEpoch`
- `policyGeneration`

每个文档实例还有自己的内存 DOM、令牌表、定时器、网络请求、MessagePort、Promise 回调和延迟事件队列。

```text
live ──pagehide(persisted)──▶ frozen ──pageshow──▶ restoring
  │                              │                    │
  │                              │             校验通过│校验失败
  │                              │                    ▼
  │                              │                  live
  │                              │             或 discarded ──▶ fresh navigation
  │                              │
  └──AuthEpoch Revoked──────────┴──▶ locked / discarded
```

- `frozen`：所有敏感能力关闭，只有响应、消息、Promise 回调和 timer 到达事件被绑定到旧 generation 后排队。
- `restoring`：文档 lane 不得运行任何任务。`pageshow` 校验在 control lane 上完成。
- `discarded`：DOM、快照、令牌、请求体、端口和延迟队列全部清理；旧 generation 的任务被取消或执行时拒绝。
- `locked`：可见文档所在会话被撤销，但与 BFCache 无关；所有敏感能力立即拒绝，等待全新导航。

## 认证事件

`AuthEpochEvent` 由账户认证服务以 Ed25519 签名，签名载荷恰好是：

```ts
{
  domain: "bfcache-auth-epoch",
  version: 1,
  accountID,
  authSessionID,
  epoch,
  state, // "Active" | "Revoked"
}
```

`AccountAuthService` 提供：

- `issueActive()`：创建 Active 事件。
- `revoke()`：注销，epoch 递增并 Revoked。
- `resetPassword()`：撤销账户下所有 Active 会话。
- `raiseRisk()`：风险升级，同样递增 epoch 并 Revoked。
- `bind(host)`：把新签名事件推入宿主 control lane。

宿主对每个 authSession 保留单调 epoch：

- 旧 epoch 事件拒绝（`stale-epoch`）。
- 同 epoch 不同签名/状态拒绝（`epoch-conflict`）。
- Revoked 会话不能再用更高 epoch 的 Active 事件复活（`revoked-session-terminal`）。
- 签名错误、字段缺失或账户不匹配的事件不会改变任何状态。

恢复时会再次调用 `AuthEpochAuthority.readCurrent()`，即使宿主缓存中已有答案。缓存只用于拒绝陈旧/回滚答案，不能替代重新读取。

## 恢复判定

仅当以下条件全部成立才解冻：

1. 重新读取的 `AuthEpoch` 签名有效；
2. 原 `authSessionID` 仍为 `Active`；
3. 当前 epoch 等于文档绑定的 epoch，且没有观察到更高 epoch 或回滚；
4. 账户、top-level site、origin、browserSessionID 全部匹配；
5. 重新读取的策略 generation 与文档绑定值一致；
6. 策略能力指纹（DOM/网络/表单/MessagePort）完全一致；
7. 文档没有因已观察到的 Revoked 事件进入 quarantine。

任一条件失败都会：

- 在同一个 control 任务内把旧文档标为 `discarded`；
- 清除内存 DOM 快照、DOM、令牌、网络响应体、端口和延迟队列；
- 取消旧 generation 的文档 lane 任务；
- 立即创建新 `documentGeneration` 的全新文档（没有旧 DOM/脚本）；
- 返回带 `reason`、`details` 和 `droppedDeferred` 的 `ResumeResult`。

旧脚本的 `pageshow` 监听器不会在失败恢复路径上运行。成功恢复时才先触发 `pageshow`，再按到达顺序投递冻结期间的事件。

## 冻结能力

`pagehide({ persisted: true })` 的同步顺序：

1. 向页面分发 `pagehide`（页面此时仍可保存自己的状态）；
2. 在内存中创建 DOM 快照；
3. 暂停 timer 队列；
4. 关闭新网络请求、表单提交和 MessagePort 投递；
5. 文档进入 `frozen`。

定时器在恢复后按冻结时长平移 deadline，不会在恢复瞬间补发。

DOM 快照对象 `MemoryOnlyDomSnapshot`：

- 只存在内存；
- `storage` 固定为 `"memory"`；
- `toJSON()` 直接抛错，禁止序列化到 localStorage / IndexedDB / 网络；
- discard 时 `wipe()`。

## 延迟事件与确定性事件循环

`DeterministicEventLoop` 有两个 FIFO lane：

- `control`：AuthEpoch、policy、pageshow 校验；
- `document`：timer、网络响应、Message、Promise 回调。

**control lane 永远优先于 document lane。** 这保证：

- 网络响应即使先到达，只要注销事件也已注入，注销会先被处理；
- 恢复校验期间文档 lane 不会运行；
- 恢复失败时旧任务在全新导航之前不会执行。

冻结期间到达的事件记录 `{arrivalSeq, documentGeneration}`，进入文档的 deferred 队列。恢复成功时按 `arrivalSeq` 排序后重新进入 document lane；恢复失败时全部丢弃。

`GuardedPromise` / `queuePromiseCallback` 用于 Promise 回调，因为浏览器原生的 microtask 不能被 BFCache 冻结，宿主适配层必须把回调路由到 generation fence。

## 最短违规轨迹搜索

`findShortestViolationTrace` 对候选实现的 guard 配置做 BFS，每个前缀在全新宿主中重放，并利用状态去重寻找最短轨迹。

```ts
import { createReviewHarness, findShortestViolationTrace } from "./index.ts";

const trace = await findShortestViolationTrace(
  () =>
    createReviewHarness({
      reviewGuards: {
        revalidateOnResume: false,
        authorizeSensitiveEffects: false,
        purgeOnRevokedEvent: false,
        quarantineOnRevokedEvent: true,
        checkDeliveryAgainstAuth: true,
      },
    }),
  { maxDepth: 6, maxStates: 50000 },
);
// trace.depth === 5
// trace.actions === ["pagehide", "revoke", "pageshow", "drain", "domRead"]
// trace.violation.type === "sensitive-dom-read"
```

典型结果：

- **投递晚于注销**：`["revoke", "lateResponse", "drain"]`，如果实现没有在 document lane 投递前复查授权。
- **恢复不复查**：`["pagehide", "revoke", "pageshow", "drain", "domRead"]`，如果实现恢复时跳过 AuthEpoch/策略/分区校验。
- **生产 guard 集**：搜索范围内返回 `null`，即未找到违规轨迹。

如果只想审查 BFCache 路径，使用 `findShortestBfcacheViolationTrace()`（或给
`findShortestViolationTrace` 传 `requiredActions: ["pagehide", "pageshow"]`）。
它要求违规轨迹确实经历冻结和恢复，例如：

```ts
const bfcacheTrace = await findShortestBfcacheViolationTrace(
  () => createReviewHarness({ reviewGuards: { /* candidate */ } }),
  { maxDepth: 6, maxStates: 50000 },
);
// ["pagehide", "revoke", "pageshow", "lateResponse", "drain"]
```

`reviewGuards` 只用于差分审查。生产宿主默认使用 `SAFE_GUARDS`，不要传入 `reviewGuards`。`UnsafeUnverifiedAuthAuthority` 同样只用于确定性轨迹搜索，绝不能用于真实宿主。

## 主要 API

| API | 作用 |
| --- | --- |
| `createBfcacheHost(options)` | 创建宿主状态机 |
| `host.navigate(input)` | 读取并校验认证/策略，创建新文档 |
| `host.pagehide(opts)` | 同步触发 pagehide 并冻结/销毁 |
| `host.pageshow(req)` | 在 control lane 校验并恢复/丢弃文档 |
| `host.enqueuePageshow(req)` | 只排队，不驱动事件循环 |
| `host.receiveAuthEpoch(event)` | 排队、验签并应用 AuthEpoch |
| `host.receivePolicy(policy, partition?)` | 推送策略收紧 |
| `host.readSensitive(selector)` | 受控敏感 DOM 读取 |
| `host.readSensitiveToken(name)` | 受控内存令牌读取 |
| `host.submitForm(action)` | 受控表单提交 |
| `host.startNetworkRequest(url, handler?)` | 受控网络提交 |
| `host.deliverNetworkResponse(id, body)` | 绑定 generation 的响应到达 |
| `host.createMessagePort()` / `deliverMessage()` | 受控 MessagePort |
| `host.createPromise()` / `queuePromiseCallback()` | 受控 Promise 回调 |
| `host.setTimeout()` / `advanceTime()` | 可冻结的确定性 timer |
| `host.runUntilIdle()` | 排空两个 lane |
| `findShortestViolationTrace()` | BFS 搜索最短违规轨迹 |
| `findShortestBfcacheViolationTrace()` | 限定必须包含 pagehide/pageshow 的最短违规轨迹 |

## 测试覆盖

`test/host.test.ts` 覆盖：

- Ed25519 签名、篡改、跨密钥验证、epoch 单调与 replay；
- 冻结后 DOM/网络/表单/MessagePort/timer 全部拒绝；
- 恢复成功后响应、消息、Promise 按到达顺序投递；
- 注销后恢复丢弃、清理并全新导航；
- 同会话策略收紧（重新读取和推送两种路径）；
- 跨站后退；
- 多个历史项独立 fencing；
- 响应先于注销到达、但投递晚于注销；
- 失败恢复前旧脚本不运行任何任务；
- 最短轨迹搜索找到投递/恢复两类缺陷，并对生产 guard 返回 `null`。

Node：

```bash
npm test
```

Bun 本地：

```bash
bun test test/host.test.ts
```

## 集成边界

本包提供安全核心和确定性模型，不直接接管浏览器对象：

- 宿主适配层需要在真实 `pagehide` / `pageshow` 事件上调用对应方法；
- DOM 适配层应实现 `DomAdapter`，不能把快照写入持久存储；
- 网络/MessagePort/Promise 适配层必须把到达事件交给 `deliver*` / `queue*`，不能直接调用旧页面回调；
- authority 的 `readCurrent` 必须是服务器/认证服务的实时答案，不能是页面可读的本地缓存。

只要适配层绕过这些入口直接执行旧脚本或原生回调，状态机无法替它兜底；这正是 `findShortestViolationTrace` 用来审查的缺陷类型。
