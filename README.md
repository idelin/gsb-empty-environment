# sessionrouter

本地会话路由状态机（Go，仅标准库，兼容 Go 1.11）。

## 设计

- `router.go` — 核心状态机。每个会话记录 `sessionID / generation / owner / 过期tick / 路由令牌`。
  - **创建**：从健康节点按确定性哈希（FNV-1a，对 `sessionID+generation` 取模，节点排序后选取）路由。
  - **续期/完成/写回**：必须携带当前 generation，旧代际一律拒绝（`stale-generation`）。
  - **故障迁移**：`NodeDown` 把受影响会话迁到健康节点，generation 递增并签发新路由令牌；无健康节点时会话被强制关闭。旧节点恢复（`NodeUp`）不会取回会话，其迟到的旧代际通知全部被拒。
  - **幂等**：所有请求按请求 ID 记录首个决定并持久化；同 ID 同参数返回相同决定（`Replayed=true`），同 ID 不同参数返回 `request-id-conflict`。
  - **TTL**：每个 tick 先处理到期再处理请求（`ExpiresAt <= now` 即到期）。
  - **重建**：会话关闭后可重建，generation 严格大于历史最高水位。
  - **决策输出**：`Decision` 携带 `Code`/`Reason`（决策依据）与 `ActiveSessions`（活跃会话计数）；所有状态变更写入持久化事件日志。
- `store.go` — 持久化。`MemStore`（测试用）与 `FileStore`（JSON 原子写入 + 独立 highwater 文件）。崩溃恢复时以 generation 高水位为下限，过期备份无法回退 generation。
- `clock.go` — 注入式 `Clock` 接口与手动推进的 `TestClock`。
- `model_test.go` — 独立串行参考模型；随机操作序列（4000 步，含故障/恢复/重复迁移/TTL 边界/旧通知）与 Router 逐步比对决定与最终状态。
- `router_test.go` — 场景测试：确定性选主、代际校验、故障迁移与迟到通知、重复迁移幂等与冲突、TTL 同 tick 边界、关闭重建、全部节点不可用、崩溃恢复与旧备份回滚防护、事件日志时序。

## 测试

```sh
GO111MODULE=on go test ./...
```
