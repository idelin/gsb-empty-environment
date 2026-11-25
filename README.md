# pinpolicy — 固定策略发布验收器

可导入的 Go 包 `pinpolicy`（模块 `prescreen.local/ps-9059846159`），用于离线终端从旧固定信任根迁移到新信任根时，对签名策略发布做**穷举式**验收，而不是只验证当前一次连接。

## 验收的两条性质

- **安全性 Safety**：在每个合法上线窗口、每个可达检查点、时钟误差区间内的每个 skew 下，终端都无法接受被撤销的钥、备用键回滚策略，或缺少双根交接授权的迁移。
- **活性 Liveness**：每个合法上线窗口（已有迁移目标发布、且存在满足离线上限的检查点）至少存在一条可验证迁移链：序号连续、上一策略摘要相接、角色门槛由在线密钥满足，根切换步骤带完整交接授权。

## 时间与边界语义

时间是离散小整数，所有边界显式可核验：

| 事件 | 语义 |
| --- | --- |
| 策略发布 `PublishAt` | 闭，`t >= PublishAt` 可取 |
| 策略窗 `ValidFrom..ValidUntil` | 两端闭，按本地时钟 `t+skew` 判断 |
| 密钥撤销 `RevokedAt`（`Revoked=true`） | 开，在线时刻 `t < RevokedAt` 才存活；恰在上线点撤销即拒绝 |
| 交接授权 `PublishAt` | 闭，且旧根/新根签名钥在真实上线时刻都必须存活 |

真实时间线按全部事件端点切成**单点 `point`** 与相邻端点间的**开区间 `open`**。相邻整数间的开区间不含任何整数上线时刻，标记 `Empty=true` 并保留在报告中；非空开区间取最小内部整数作为确定性样本。这样相等点（闭/开边界判定）与开区间内部行为严格区分。

## 设备模型

- 检查点 `c` 在真实上线时刻 `t` 可达当且仅当 `0 <= t-c <= OfflineMax`。
- 本地时钟枚举 `skew ∈ [SkewMin, SkewMax]` 内每个整数，本地时间为 `t+skew`。
- 未发布内容对终端不可见，发布可见性只由真实时间决定，不受时钟误差影响。
- 策略对同一主机单调编号；同一主机出现两个可获取的同序号策略即冲突，拒绝接受。

## 回滚规则

当同一主机存在已发布的更高序号策略时，低序号策略为“已废止（obsolete）”。任何由备用公钥（`Signature.Backup=true`）承载的已废止策略都判定为 `backup_key_rollback` 并拦截；备用钥永远无法复活被取代的策略。

## 交接授权规则

根切换到 `NewRoot` 的策略，其每个门槛角色都必须存在一条交接记录，且同时满足：

- 同一 `Host`；
- 同一 `PolicyDigest`（`pinpolicy.Digest(policy)` 的规范 SHA-256）；
- 同一交接角色；
- 一个存活的旧根钥与一个存活的新根钥分别签名。

两份各覆盖一半角色的策略无法拼出授权，会分别在缺失角色上报告 `handover_authorization_missing`，并给出缺失侧（`old`/`new`/`both`）。

## 用法

```go
report, err := pinpolicy.Verify(in)
if err != nil { /* 结构性输入错误 */ }

report.Pass         // safety && liveness
report.SafetyPass
report.LivenessPass
report.Elements     // 端点分区，逐项可核验
report.Windows      // 每个元素的检查点×skew 用例与逐发布裁决
report.EarliestFailure // 失败时最早的时间元素、检查点、skew 与缺失授权

fmt.Print(pinpolicy.RenderText(report)) // 确定性文本报告
```

输入字段见 `pinpolicy.Input`，失败原因枚举见 `Reason*` 常量。

## 穷举对照测试

`go test ./...` 使用小整数时间覆盖要求的四个对照场景及边界：

- `TestCrossWindowRenewal`：跨生效窗续签，全 skew 下链仍可验证；
- `TestBackupKeyRollback`：备用键承载已废止序号，每个可获取窗口都被拦截而合法链存活；
- `TestSplitRolesAcrossTwoPolicies`：两份各得半数角色的策略，活性失败并点名缺失交接角色；
- `TestRevocationExactlyAtOnlineBoundary`：撤钥恰在上线点，相等点拒绝、相邻空开区间显式标注；
- 另含缺失交接侧、未知/已撤销签名者、序号冲突、离线不可达、摘要断链与输入校验测试。

## 验证命令

```sh
go test -race -cover ./...
go vet ./...
```
