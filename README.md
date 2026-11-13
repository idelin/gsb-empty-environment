# RPKI缓存回滚到旧验证载荷

路由器从RPKI缓存同步验证载荷，攻击者可重放旧session/serial视图，让已撤销ROA再次生效。给Go路由宿主实现题内缓存同步包：每个cacheID有随机sessionID、32位serial和VRP集合(prefix,maxLength,originAS)，路由器持已提交session/serial/集合摘要与更新时间。Reset Query返回完整快照Begin(session,serial)、若干VRP、End(session,serial,rootDigest)；Serial Query返回从旧serial连续到新serial的Add/Remove事件及End。所有消息由缓存Ed25519签名并绑定cacheID、传输连接代次和规范内容；同session内serial按模2^32前向距离1至2^31-1才算更新，跳号、倒退或重复不同内容拒绝。session变化只能走完整Reset，期间旧已提交集合继续服务但标stale，完整root校验后原子切换；增量在私有副本应用，Remove不存在项或Add冲突拒绝。缓存长时间stale后路由策略按宿主配置fail-closed或unknown，不继续称valid。实现断线续传、响应乱序和双缓存各自独立状态；切换优先缓存只接受完整已提交视图，测试旧session重放、serial回绕、缺事件、根不符和切换中途崩溃，任何查询只看到完整旧或完整新集合。

## 实现

包名为 `netsec`，无第三方依赖。核心入口：

- `NewCacheWithConfig` / `NewCache`：缓存端状态机。默认生成随机 128 位 session；VRP 集经过规范化、去重和排序。
- `Cache.HandleQuery`、`ResetResponse`、`SerialResponse`：生成逐片 Ed25519 签名消息。签名覆盖 cacheID、连接代次、requestID、消息类型、分片索引、session、serial、边界、VRP 和 root digest。
- `NewRouter`：注册受信任缓存公钥，配置 `StaleAfter`、`MaxStale` 和 `StalePolicy`（`PolicyFailClosed` / `PolicyUnknown`）。
- `Router.ResetQuery` / `SerialQuery` / `IngestMessage` / `IngestResponse`：查询与验证入口。响应可乱序到达，完整校验后一次性提交。
- `Router.View` / `Validate` / `PreferredView`：只返回完整已提交视图；stale/expired 状态不会报告为 `valid`。
- `SaveState` / `LoadState` / `ExportState` / `RestoreState`：原子写文件和崩溃恢复。恢复只包含完整提交视图，并递增连接代次。

安全边界：

- 同一 session 内只接受模 2^32 前向距离 1..2^31-1；重复 serial 只有 root 完全一致才可无操作接受。
- 增量必须覆盖旧 serial 到新 serial 的每一级；Add 冲突、Remove 不存在、root 不符均在私有副本上失败，旧视图不变。
- session 变化只允许完整 Reset。旧 session 进入持久化的 retired 集合，之后即使重新签名也会被拒绝。
- Reset 分片期间旧视图继续可读但标记 `stale`；新集合通过根校验后在其临界区内原子替换。
- 传输代次、随机 requestID 和 cacheID 都参与签名，断线重连会丢弃未完成响应并拒绝旧代次消息。

运行验证：

```sh
go test ./...
go test -race ./...
go vet ./...
```
