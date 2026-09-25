# at-status-queue

辅助技术（assistive technology）状态消息队列：区分礼貌（polite）/紧急（urgent）优先级，
支持合并键、过期、取消、撤回与幂等确认。时钟可注入，核心计算不依赖浏览器布局引擎。

纯 TypeScript，零依赖；需要 Node.js ≥ 23.6（内置类型擦除 + `node:test`）。

## 运行

```sh
npm test        # node --test（Node.js 内置测试运行器）
npm run sample  # node examples/demo.ts（可控时钟演示）
```

## 设计

- `src/clock.ts` — `Clock` 接口（`now`/`setTimeout`/`clearTimeout`），
  `SystemClock` 与可单步推进的 `ManualClock`。引擎不直接触碰全局时钟。
- `src/types.ts` — 每条消息携带状态实体 ID、实体版本、确认 ID（ackId）、
  优先级、TTL、可选合并键（mergeKey）与所属组件（ownerId）。
- `src/announcer.ts` — 队列引擎 `StatusAnnouncer`：
  - 紧急消息打断正在播报的礼貌消息；被打断的礼貌消息在恢复前
    重新校验时效（过期 / 实体撤回 / 版本过旧则丢弃，绝不播过时消息）。
  - 相同 mergeKey 的同进度消息合并，保留最新。
  - 投递失败按 `retryDelayMs` 重试；确认按 ackId 幂等，重试不会重复确认。
  - `correctEntityVersion` 版本更正、`retractEntity` 实体撤回、
    `cancelEntity`/`cancelMessage` 取消、`unmount` 组件卸载，
    都会使排队 / 暂停 / 播报中的相关消息失效。
  - 公平调度：连续紧急播报有上限（`urgentStreakLimit`，含打断行为），
    紧急流量不会永久饿死礼貌消息。
- `src/layout.ts` + `fixtures/` — 文本测量、贪心换行、状态区定位均为纯函数，
  几何数据全部来自夹具（节点树 + 字形度量），不借浏览器布局引擎。

## 运行样例（`npm run sample`）

```
[t=   0ms] --- polite progress announcement starts ---
[t=   0ms] SPEAK   (polite) "Uploading photos: 10 percent"
[t=  30ms] --- urgent message interrupts; polite is paused ---
[t=  30ms] PAUSE   "Uploading photos: 10 percent"
[t=  30ms] SPEAK   (urgent) "Connection lost"
[t=  30ms] --- while urgent speaks: same-mergeKey updates coalesce (newest wins) ---
[t=  30ms] DROP    "Uploading photos: 20 percent" (merged)
[t=  30ms] --- ...and a short-lived update expires before its turn ---
[t=  86ms] ACK     ack-2
[t=  86ms] DONE    "Connection lost"
[t=  86ms] RESUME  "Uploading photos: 10 percent"
[t= 166ms] ACK     ack-1
[t= 166ms] DONE    "Uploading photos: 10 percent"
[t= 166ms] SPEAK   (polite) "Uploading photos: 40 percent"
[t= 276ms] ACK     ack-4
[t= 276ms] DONE    "Uploading photos: 40 percent"
[t= 276ms] DROP    "Uploading photos: 41 percent" (expired)
[t= 330ms] --- entity retraction invalidates in-flight and queued messages ---
[t= 330ms] SPEAK   (polite) "Sync will start soon"
[t= 330ms] DROP    "Sync details follow" (entity-retracted)
[t= 330ms] DROP    "Sync will start soon" (entity-retracted)
[t= 330ms] --- version correction invalidates older messages ---
[t= 330ms] SPEAK   (polite) "Uploading photos: 60 percent"
[t= 330ms] DROP    "Uploading photos: 60 percent" (stale-version)
[t= 330ms] SPEAK   (polite) "Uploading photos: 70 percent"
[t= 440ms] ACK     ack-9
[t= 440ms] DONE    "Uploading photos: 70 percent"
[t= 440ms] --- queue drained ---

layout check: "Uploading photos: / 70 percent" fits status-region (width 160px, text 238px)
```

## 测试结果（`npm test`）

20 个测试全部通过：

- 礼貌消息 FIFO 投递与完成回包（ack）
- 紧急打断礼貌、暂停后恢复（恢复前重验时效）
- 暂停期间过期 / 实体撤回 → 不恢复、不播过时消息
- 版本更正使排队消息失效、拒绝过旧版本入队
- 相同 mergeKey 合并保留最新；过期消息不播报
- 取消状态 / 组件卸载清理所属待播项（含播报中）
- 重试投递不重复确认；重试期间过期则丢弃
- 紧急连播有界，礼貌消息不饿死（活性）
- 可控时钟 + 确定性随机的 400 轮工作负载：断言播报时消息必为存活、
  ack 唯一、每条被接受的消息最终到达且仅到达一个终态
- 布局：字形测量、换行、夹具节点树状态区定位（无浏览器参与）
