# 断线续传模拟器（Resumable Stream Simulator）

模拟"事件流订阅 + 断线续传"协议：客户端断线后携带游标（cursor）重连，服务端校验游标并选择
增量重放或快照恢复；服务端日志可能被压缩（compaction）清理旧记录。

## 协议流程

```
客户端                                服务端
  |------ connect(filter, cursor?) --->|
  |                                    | 1. 校验游标（HMAC 签名 / 版本 / 过滤条件哈希）
  |                                    | 2. 游标有效且日志未压缩到该位置 → 增量重放 (catchUp)
  |                                    | 3. 游标过旧（记录已被压缩）→ 快照恢复：
  |                                    |    a. 记录高水位 H = head()，并对 H+1 加 pin
  |                                    |    b. 异步生成快照（期间事件继续产生）
  |<----- snapshot(epoch, baseSeq=H) --|    c. 发送快照 + 新游标（新 epoch，offset 从 0 起）
  |<----- event(offset=0,1,2...) ------|    d. 从日志重放 seq > H 的事件（切换处无遗漏）
  |<----- event(live) -----------------|    e. 挂接实时监听，后续事件实时推送
```

## 关键设计

- **游标**（`src/cursor.ts`）：`base64url(payload).hmac`，payload 为
  `{ v, fh, seq, off, ep }` —— 版本、订阅条件哈希、全局序号、epoch 内偏移、epoch。
  签名错误、版本不支持、格式非法分别抛出带 `reason` 的 `CursorError`；
  订阅条件不一致抛出 `CursorFilterMismatchError`（**游标不可跨订阅条件复用**）。
- **压缩日志**（`src/event-store.ts`）：`compact(beforeSeq)` 丢弃旧事件并推进 `baseSeq`；
  `pin(seq)` 保护快照恢复期间 `seq > H` 的事件不被压缩掉。
- **快照与增量切换**（`src/server.ts`）：快照内容取自高水位 H 的不可变状态，
  快照生成期间新产生的事件（seq > H）通过日志重放补齐，保证切换处不遗漏、不重叠。
- **重复可识别**（`src/client.ts`）：每条事件消息携带 `(epoch, offset)` 与唯一事件 id，
  offset 在 epoch 内严格连续；`offset < expectedOffset` 或 id 已见即判定为重复，
  `offset > expectedOffset` 判定为缺口（anomaly）。每条消息附带续传游标。

## 运行

```bash
npm install   # 需要 typescript 与 @types/node
npm test      # tsc 编译后运行测试
```

测试覆盖（`test/`）：

- 保留日志内的断线续传：只重放错过的事件，offset 连续；
- 游标过旧：返回快照 + 增量起点，最终状态与服务端一致；
- 快照生成期间持续产生事件：切换处无遗漏（逐条比对 `seq > H` 的事件）；
- 快照期间发生压缩：pin 保护重放所需事件；
- 过期游标重连：重放事件被客户端明确识别为重复且不重复应用；
- 跨订阅条件复用游标 / 篡改游标 / 非法游标：分别抛出对应错误；
- 过滤订阅：offset 在过滤后的事件流上仍然连续。
