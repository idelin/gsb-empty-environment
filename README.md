# 混合密钥交换降级固定

混合 X25519MLKEM768 握手里，在线攻击者可让握手落到只有 X25519 的组上，例如把混合组从 ClientHello 中剥离。为Go客户端写供连接建立调用的 PQ 能力固定包：对曾协商过混合组的服务端记录固定期限，期限内若 ServerHello 或 HelloRetryRequest 选了非混合组、或恢复会话的原始握手不是混合组，就中止并报告降级；固定记录只能由已完成且验过 Finished 的混合握手写入。用剥离、HRR 改选、过期边界、旧经典会话恢复和未固定的主机验证。

## 实现：`pqpin` 包

`pqpin/` 提供面向连接建立的后量子密钥交换能力固定（capability pinning）：

- `Pinner` 以 `host:port` 为键保存固定记录（协商的混合组、创建与过期时间），默认固定期 7 天，可通过 `WithTTL` 覆盖，`WithClock` 注入时钟便于测试过期边界。
- `Dialer` 包装标准 `crypto/tls`：用 `snoopConn` 只读嗅探明文 ServerHello / HelloRetryRequest 的 `key_share` 选中组，不改动字节流；握手成功（对端 `Finished` 已验证）后才允许写固定。
- 固定期内出现以下任一情况即返回 `*DowngradeError` 并中止连接：
  - 最终 ServerHello 选中非混合组（含 ClientHello 混合组被剥离后回退到 X25519）；
  - HelloRetryRequest 选中非混合组，且在嗅探到 HRR 的当下就通过回调切断连接；
  - 恢复的会话其原始握手不是混合组（来源标记存于 `tls.SessionState.Extra`，随票据持久化）。
- 固定记录只能由 `Commit` 写入，而 `Commit` 拒绝任何非混合协商；失败握手或被判定降级的握手都不会创建、刷新或延长固定。

### 关键文件

- `pqpin/pinner.go`：固定存储、过期判定与 `Evaluate`/`Commit` 强制逻辑。
- `pqpin/snoop.go`：TLS 记录流解析，识别 ServerHello 与 HRR 的选中组。
- `pqpin/sessioncache.go`：包装 `tls.ClientSessionCache`，把“原始握手是否混合”写入会话状态。
- `pqpin/dialer.go`：连接建立入口，串联嗅探、HRR 即时中止、评估与提交。
- `pqpin/*_test.go`：单元测试与进程内 TLS 1.3 端到端场景。

### 验证场景

测试覆盖：ClientHello 混合组剥离后的阻断、HRR 改选经典组、固定过期的纳秒级边界（到期时刻仍有效、过后失效并可重新学习）、旧经典票据恢复被拒、混合票据恢复放行，以及未固定主机对经典协商放行且不产生固定。
