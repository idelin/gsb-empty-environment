# FTP数据通道身份绑定

文件传输代理不能把另一连接抢先建立的数据通道接到已授权操作。写供Go安全代理调用的FTP式通道组件，控制会话中的每次传输批准固定操作摘要、账号、被动或主动模式及允许对端集合，数据通道只有匹配一次性传输实例才可接入；取消、重新认证与下一次传输不得复用旧批准。用可暂停连接器测试被动端口抢占、主动目标替换和迟到数据连接，未授权对端收到零正文。

## 组件

`package netsec`（`module example.org/netsec/q0083`）为安全代理提供把控制会话授权与
数据通道严格绑定的 FTP 式原语。

- `Approval`：一次性传输批准，固定操作摘要 `Digest`、账号 `Account`、
  通道模式 `Mode`（`Passive`/`Active`）与允许对端集合 `AllowedPeers`。
- `Session.Authorize`：每次批准都铸造一个带唯一 `ID` 与认证纪元 `Epoch` 的
  `Transfer` 实例；批准不会在实例间复用。
- `Transfer.OpenPassive` / `Transfer.ConnectActive`：仅按批准模式建立通道，
  模式不符返回 `ErrModeMismatch`，主动目标不在对端集合返回 `ErrPeerNotAllowed`。
- `Transfer.Attach`：只交付一次数据连接；并发或重复的接入者收到
  `ErrTransferInUse`。
- `Transfer.Cancel` / `Complete`、`Session.Cancel` / `Reauthenticate` /
  `Close`：撤销实例并关闭监听，撤销后迟到连接一律被关闭，旧批准不可再接入。
- `TCPConnector`：生产环境真实 TCP 实现；`Connector` 接口允许注入测试传输。

## 安全保证

- **实例唯一性**：一个 `Transfer` 至多接入一条连接，连接对端必须精确命中
  `AllowedPeers`（`netip.AddrPort` 全等比较，含端口）。
- **抢先连接隔离**：被动端口上先到的非授权连接在 `acceptLoop` 中立即关闭，
  不占用实例，授权对端随后仍可接入；非授权对端读不到任何正文。
- **主动目标固定**：主动拨号的目标在拨号前校验，拨号可被暂停但无法把批准
  重定向到集合外地址；集合外目标不产生任何连接。
- **撤销不可复用**：取消、重新认证（纪元递增）和会话关闭都会令实例终止，
  之后的 `Attach` 返回 `ErrTransferRevoked`；下一次传输需要新批准并获得新端点。

## 可暂停连接器与测试

`PausableConnector` 用 `net.Pipe` 替代真实套接字，提供确定性测试能力：

- `PauseDials`/`ResumeDials` 阻塞或放行主动拨号，并暴露 `PendingDials`、
  `Dialed` 以检查实际目标；
- `PausableListener.PauseAccept`/`ResumeAccept` 控制 `Accept` 出队，
  `Inject(remote)` 以指定对端身份注入数据连接；
- 监听关闭后，积压与新注入的连接都被关闭，模拟迟到连接收到零正文。

测试场景：

- `TestPassivePortPreemption`：攻击者先连被动端口被拒且零正文，授权对端正常接入。
- `TestActiveTargetReplacement`：暂停中的拨号不能替换目标，集合外目标被拒绝，
  取消后的暂停拨号无法接入。
- `TestLateDataConnection`：取消、重新认证、会话取消/关闭后的迟到连接零正文，
  旧批准不可复用，新传输获得独立实例与端点。
- `TestConcurrentAttachOneWinner`：数据先到或先 `Attach` 两种时序下，
  并发接入者中仅一个获胜。
- `TestTCPConnectorLoopback`：真实 TCP 端到端冒烟（沙箱禁止 bind 时自动 `Skip`）。

运行：

```sh
go test -race ./...
```
