# 路径验证组件设计（package `netsec`）

面向连接迁移的传输宿主，提供类似 QUIC `PATH_CHALLENGE` / `PATH_RESPONSE`
的路径验证能力。核心目标：移动网络切换后，一条路径上的验证响应绝不能认证
另一条候选地址。

## 三重绑定

每个挑战（`issueChallengeLocked`）同时绑定：

1. 连接实例 `ConnID` —— 网络切换产生新实例，旧实例的响应在
   `HandleResponse` 入口即被 `ErrStaleConnection` 拒绝；
2. 候选路径 `Path{Conn, Local, Peer}` —— 响应的本地套接字和对端源地址必须
   与已注册候选完全一致，否则返回 `ErrUnknownPath`；
3. 随机标识 `Nonce`（默认 `crypto/rand` 8 字节，带跨候选去重）—— 响应的
   nonce 必须是该路径上“仍在等待”的挑战，否则返回 `ErrNonceMismatch`。

三者同时匹配才置 `validated`，并清空该路径所有 outstanding nonce，使重放的
旧响应无法再次生效。

## 验证成功前受限发送

- `Validator.CheckSend` 要求路径存在、已验证、且为当前活动路径；
- `Host.SendData` 在调用真实数据回调前先过 `CheckSend`，未验证时数据回调
  根本不会执行，验证前的流量只允许是挑战探针；
- 探针通过 `SendFunc(path, nonce)` 发出，回调显式携带 `path.Local` 与
  `path.Peer`，测试据此核对数据报的真实收发地址。

## 策略决定唯一活动路径

多个候选可同时验证，但 `Strategy.Select` 只选出一条活动路径
（默认 `latestStrategy` 选择最近注册者，可注入自定义策略）。未被选中的已
验证路径保持“已验证但不可发”（`ErrPathNotActive`）。

## 固定截止时间，重试不延长

每个候选在 `AddCandidate` 时给定绝对 `deadline`，之后永不修改。定时器
`scheduleLocked` 将下一次触发钳制到 `deadline`；`onTimer` 到时点之后判定
`ReasonDeadline` 失败并删除候选，重试次数再多也不会推后总截止时间。

## 时钟与并发

- `Clock`/`Timer` 抽象时间，生产用墙钟，测试用 `FakeClock`（手动 `Advance`，
  按截止时间有序触发，支持 `Stop`/`Reset`）；
- 所有公开方法持锁修改状态，把发送与事件回调收集进 `actions`，在解锁后执行，
  避免回调重入死锁；测试以 `-race` 运行。

## 测试覆盖（假时钟）

- 地址往返与发送回调真实地址；
- 旧连接 ID 复用 / 迁移后重放拒绝；
- 乱序响应、跨路径 nonce 拒绝；
- 重试不延长截止时间、迟到响应；
- 单候选取消、`Close`、活动路径取消后的策略回退；
- 多候选下仅策略所选路径可活动。

语句覆盖率 100%，`go vet` 与 `go test -race` 通过。
