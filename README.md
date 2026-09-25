# status-announce-queue

辅助技术（屏幕阅读器）状态消息队列：区分礼貌（polite）/紧急（urgent）优先级，
支持合并键、过期、取消、实体撤回、版本更正与确认去重；时钟可注入，
全部核心计算（含文本测宽）不依赖浏览器布局引擎，由夹具提供节点树与字形度量。

## 运行环境

- Node.js ≥ 23.6（直接运行 TypeScript，类型擦除）；或 Node ≥ 22.6 加
  `--experimental-strip-types`。仓库内含 `.nvmrc`（v23.9.0）。
- 无第三方依赖；测试使用 Node.js 内置测试运行器（`node:test`）。

```sh
nvm use          # 可选，切换到 .nvmrc 指定版本
npm test         # 运行全部测试
npm run demo     # 运行场景样例
```

## 设计要点

- **可注入时钟** `Clock`（`src/clock.ts`）：`SystemClock` 生产用，`ManualClock`
  测试/演示用；过期判断只读注入时钟。
- **消息模型**（`src/queue.ts`）：每条消息携带状态实体 `entityId`、`version`、
  确认 ID `ackId`、所属组件 `ownerId`、优先级、可选 `mergeKey`/`progress`/`ttlMs`。
- **打断与恢复**：紧急消息到达时暂停在播礼貌消息；礼貌消息恢复前重新校验
  时效（过期 / 实体撤回 / 版本过旧即丢弃，绝不播过时消息）。
- **确认去重**：`ackId` 只回包一次；运输层重试（相同 `ackId` 再入队）返回
  `duplicate-ack`，不重复播报、不重复确认。
- **版本更正**：同一实体更高版本入队会使已排队/在播的旧版本失效（`superseded`）；
  迟到的旧版本直接 `dropped-stale`。
- **合并**：相同 `mergeKey` 且相同 `progress` 的消息合并，保留最新。
- **公平调度**：连续播报 `urgentQuota`（默认 2）条紧急消息后，让位于等待中的
  礼貌消息，保证紧急流量不会永久饿死礼貌消息，存活消息最终都会被处理。
- **组件卸载**：`unmount(ownerId)` 清理该组件所有待播项。
- **实体撤回**：`retractEntity(entityId)` 使该实体已排队消息全部失效，
  且之后的新消息直接 `dropped-retracted`。
- **文本测宽**（`src/metrics.ts`、`src/nodeTree.ts`）：从夹具节点树提取状态文本，
  用夹具字形度量表累加 advance 计算宽度并截断，纯计算、无浏览器布局引擎。

## 运行样例（`npm run demo` 输出）

```text
[t=1000] measured status text: "上传进度 42%"
[t=1000] ANNOUNCE polite "上传进度 42%"
[t=1000] DROP     "上传进度 42%" (superseded)
[t=1000] ANNOUNCE polite "上传进度 43%"
[t=1000] ANNOUNCE urgent "下载失败"
[t=1030] ACK      ack-u1
[t=1030] ANNOUNCE polite "上传进度 43%" (resumed)
[t=1030] ACK      ack-p2
[t=1030] ANNOUNCE polite "同步 v1"
[t=1030] DROP     "同步 v1" (superseded)
[t=1030] ANNOUNCE polite "同步 v2"
[t=1030] DROP     "打印排队中" (retracted)
[t=1130] DROP     "小部件已更新" (unmounted)
[t=1130] ANNOUNCE urgent "紧急告警 1"
[t=1130] ACK      ack-ub1
[t=1130] ANNOUNCE urgent "紧急告警 2"
[t=1130] ACK      ack-ub2
[t=1130] ANNOUNCE polite "同步 v2" (resumed)
[t=1130] ACK      ack-v2
[t=1130] ANNOUNCE urgent "紧急告警 3"
[t=1130] ACK      ack-ub3
[t=1130] ANNOUNCE urgent "紧急告警 4"
[t=1130] ACK      ack-ub4
[t=1130] DROP     "限时提示" (expired)
[t=1130] ANNOUNCE polite "后台任务完成"
[t=1130] ACK      ack-tail
[t=1130] idle=true pending=0
```

## 测试结果（`npm test`，Node v23.9.0）

```text
✔ collectStatusText walks the fixture tree and skips hidden nodes
✔ measureWidth sums fixture glyph advances (no layout engine)
✔ truncateToWidth is a no-op when the text fits
✔ truncateToWidth fits text plus ellipsis within maxWidth
✔ truncateToWidth returns empty when even the ellipsis does not fit
✔ polite messages are announced FIFO and acked on completion
✔ urgent interrupts polite; paused polite is re-validated and resumed
✔ paused polite message that expires during urgent interruption is not re-announced
✔ canceling a pending message prevents its announcement
✔ canceling the in-flight message stops it without ack
✔ retry delivery never double-acks
✔ version correction supersedes older queued and in-flight versions
✔ entity retraction invalidates queued messages and blocks new ones
✔ same merge key and same progress coalesce, keeping the latest
✔ urgent traffic cannot permanently starve polite messages
✔ unmounting a component discards its pending announcements only
✔ expired pending messages are dropped, never announced
✔ liveness: every live message is eventually announced and acked under fair scheduling
ℹ tests 18
ℹ pass 18
ℹ fail 0
```

## 目录结构

```text
src/clock.ts        可注入时钟（SystemClock / ManualClock）
src/queue.ts        状态消息队列核心
src/metrics.ts      基于夹具字形度量的纯文本测宽/截断
src/nodeTree.ts     夹具节点树的状态文本提取
fixtures/           字形度量表与状态节点树夹具
test/               node:test 测试（可控时钟枚举各时序场景）
examples/demo.ts    可运行场景样例
```
