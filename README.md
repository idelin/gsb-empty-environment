# sessionrouter

本地会话路由状态机（Go，仅标准库）。跟踪每个会话的 `sessionID`、
`generation`、`owner`（节点）与过期 tick，节点故障时迁移会话并递增
generation、签发新路由令牌，旧代际的续期 / 完成通知 / 写回一律拒绝。

## 语义

- **创建**：从健康节点中按 FNV-1a(sessionID#generation) 确定性选择 owner；
  无健康节点拒绝。会话已关闭 / 已过期时以更高 generation 重建。
- **续期**：必须携带当前 generation；每个请求到来前先在当前 tick 执行
  到期扫描，因此同 tick 的到期优先于续期（`now >= ExpiresAt` 即过期）。
- **故障迁移**：`NodeFailed` 将会话 generation+1、重选 owner、重签令牌；
  重复故障通知是 no-op；无健康节点时迁移推迟，owner 不健康期间续期被拒。
- **恢复**：`NodeRecovered` 不回迁会话；旧节点迟到的旧代际通知全部被拒。
- **幂等**：requestID 首次绑定的决定被持久化，重放原样返回（即使期间
  发生过迁移）；同 ID 不同 session 判冲突。
- **持久化**：所有状态变更先写 WAL（JSON lines + fsync）再应用到内存，
  崩溃后重放恢复，generation 不会回退；容忍日志尾部的撕裂写。
- **可观测**：每个决定返回 `Reason`（决策依据）与 `ActiveSessions`
  （活跃会话计数）；事件日志记录创建 / 续期 / 迁移 / 过期 / 拒绝等。

## 测试

- `router/router_test.go`：故障迁移、恢复后迟到通知、重复迁移、TTL 边界
  （到期优先于同 tick 续期）、幂等重放与冲突、全部节点不可用、关闭重建、
  崩溃恢复不回退、事件日志覆盖。
- `router/model_test.go`：独立串行参考模型（切片 + 线性扫描的朴素实现），
  200 个随机种子 × 300 个随机操作逐决定比对，并核对最终会话状态与活跃计数。

运行：`GO111MODULE=on go test ./router/`
