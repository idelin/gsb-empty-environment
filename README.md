# OpenFlow主从角色仲裁

交换机代理同时连着多个控制器，被替换下来的旧主控制器仍可发流表修改。给Go交换机代理写供消息分发调用的角色仲裁包，按OpenFlow 1.3处理ROLE_REQUEST：generation_id用64位有符号差比较以支持回绕，较旧的请求返回stale；从控制器的FLOW_MOD、PACKET_OUT、METER_MOD被拒绝；异步消息只发给主和均等控制器；角色切换前已入队的旧主消息按屏障顺序丢弃。用生成号回绕、旧主竞态、从控制器写入和断线重连验证。

## `role` 包

`role`（导入路径 `example.org/netsec/q0074/role`）实现 OpenFlow 1.3.4
第 7.5.4 节的控制器角色仲裁，供交换机代理的消息分发层直接调用。

- `NewArbitrator` / `Connect` / `Disconnect` 管理控制器连接生命周期；新连接初始角色为
  `OFPCR_ROLE_EQUAL`，断线后角色、生成号与队列全部清除，重连后的首个
  ROLE_REQUEST 无条件接受。
- `HandleRoleRequest(id, role, generationID)` 处理 ROLE_REQUEST。生成号按
  64 位有符号差 `stored-req > 0` 判定新旧，天然支持无符号计数回绕；较旧请求返回
  `*RoleError{Code: ErrCodeStale}`（OFPET_ROLE_REQUEST_FAILED / OFPRRFC_STALE），
  状态不变。授予 MASTER 时旧 MASTER 自动降为 SLAVE，返回的
  `RoleDecision.Demoted` 列出被降级的控制器。
- `Enqueue` 拒绝 SLAVE 发来的 FLOW_MOD、GROUP_MOD、PORT_MOD、TABLE_MOD、
  PACKET_OUT、METER_MOD（返回 `ErrCodeBadRole`）；读/统计/屏障/ROLE_REQUEST
  仍然放行。
- `AsyncRecipients` 只返回 MASTER 与 EQUAL 控制器，结果按 ID 排序，用于
  PACKET_IN、FLOW_REMOVED、PORT_STATUS 等异步消息的扇出。
- 每条连接持有严格有序队列。MASTER 被降级时在当前队尾放置屏障水位，
  `Next` 按入队顺序把水位之前的状态修改消息标记为 `VerdictDrop`，
  屏障与读消息保持 `VerdictDeliver`，从而按屏障顺序丢弃旧主在切换前
  已入队的写消息，并保留屏障应答边界。

### 分发层调用示例

```go
arb := role.NewArbitrator()
arb.Connect("ctrl-A")

// ROLE_REQUEST（role 与 generation id 从 ofp_role_prop 中解析）
dec, err := arb.HandleRoleRequest("ctrl-A", role.RoleMaster, genID)
var roleErr *role.RoleError
if errors.As(err, &roleErr) {
    // 用 roleErr.Type / roleErr.Code 回 OFPET_ROLE_REQUEST_FAILED 错误
}

// 控制器下发消息
if err := arb.Enqueue("ctrl-A", &role.Envelope{MsgType: ofpType, Xid: xid, Payload: raw}); err != nil {
    // SLAVE 写操作被拒，回 OFPBRC_EPERM
}
env := arb.Next("ctrl-A") // 按连接顺序取，env.Verdict 决定转发或丢弃

// 交换机异步消息
for _, id := range arb.AsyncRecipients() {
    sendTo(id, asyncMsg)
}
```

### 验证

`go test ./... -race` 覆盖生成号回绕（含跨 0 与 2^63 边界）、旧主竞态与
屏障顺序丢弃、从控制器写拒绝、异步扇出、自身降级、MASTER→EQUAL、
断线重连以及 200 轮并发角色竞争（单 MASTER 不变量）。
