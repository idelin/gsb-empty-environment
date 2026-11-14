# clean session让新客户端继承了旧订阅

MQTT代理允许`clean session=false`的客户端断开后保留会话，攻击者用相同clientId接入即继承原客户端的订阅与待发消息。请实现供Go MQTT代理调用的可导入会话接管组件。会话按clientId、tenant、凭据主体与连接代次建模，接管必须由同一认证主体且凭据未撤销才能继续；不同主体使用相同clientId按拒绝或隔离处理，不得静默覆盖。遗嘱消息、QoS2未确认包和离线队列随会话迁移，跨租户与过期会话不迁移。用同ID异主体、过期会话、撤销凭据、QoS2重放、合法重连和并发接管验证订阅与消息只回到原主体。

## 实现：`sessiontakeover` 包

导入路径 `example.org/netsec/q0268/sessiontakeover`，仅依赖 Go 标准库。

### 安全模型

- 会话键为 `(tenant, clientId, subject)`，另维护租户内 `clientId -> 属主` 索引；
  每个连接分配单调递增的 `generation`（连接代次）。
- 每次 `Connect` 都经 `CredentialInspector.IsRevoked` 重新校验凭据，检查器报错按
  fail-closed 拒绝；同主体且凭据有效才恢复（resume）会话。
- 异主体使用相同 clientId：`ConflictReject` 直接拒绝并保留原会话；
  `ConflictIsolate` 给新主体独立的主体级会话，原会话与属主索引不变，绝不静默覆盖。
  原属主会话过期或终止后 clientId 才释放给新主体。
- 跨租户因复合键天然隔离，`Lookup`/离线投递只接受完全匹配的三元组。
- 会话带绝对过期时间，过期或被 `Sweep`/后台 reaper 清理后不迁移，重连只得到空会话。

### 随会话迁移的状态

- 订阅集合、遗嘱（异常断开保留、优雅断开清除）、有界离线队列。
- 出站 QoS1/2 未确认包：重连时 QoS1 以 `Dup` 重发，已收到 PUBREC 的 QoS2 改为重发
  PUBREL，PUBACK/PUBCOMP 后从账本移除。
- 入站 QoS2 重放账本：重放的 packet ID 被识别，代理只重发协议应答、不重复投递应用消息；
  有界窗口按最旧条目剪枝，账本随会话跨连接保留。
- 接管时旧连接的 `Handle` 被原子失效，后续操作返回 `ErrHandleClosed`，
  `OnDisplaced` 回调通知代理拆除旧传输层。

### 验证

`go test -race ./sessiontakeover` 覆盖：同 ID 异主体拒绝/隔离、跨租户不迁移、
过期会话不迁移、撤销/失效凭据拒绝、QoS2 重放、合法重连全状态迁移、并发接管、
遗嘱生命周期、后台清理与有界离线队列。
