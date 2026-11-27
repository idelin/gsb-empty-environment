# 所有权判定包（ownership）

面向 TypeScript 服务端的可导入纯判定库，零运行时第三方依赖，仅使用 `node:crypto`
（Ed25519）与 `node:test`。联邦收件箱中的事件可乱序、经任意投递者重复转发，
本包在每个时点给出 `publish | reject | wait` 及仍需等待的证据。

## 身份与授权模型

- 对象身份 = `{ origin, permanentId }`，二者不可变；`displayUrl` 仅为可替换展示位置，
  重新托管（`rehost`）不改变身份。同一 URL 可同时对应两个不同对象。
- `transfer` 必须同时携带旧持有人与接收者的授权签名，签名覆盖规范化事件体，
  事件体含精确父转让哈希 `prev` 与代次 `gen`。
- HTTP 传输签名（`delivery.transportSignature`）只证明投递来源，永远不能授权任何
  所有权/编辑操作；缺少授权签名的事件以 `reject_transport_only` 拒绝。
- 同一 `eventHash = sha256(canonical(body))` 经任意 relay 任意次转发都视为同一事件，
  重复投递计入 `duplicateDeliveries`，不重复判定、不改变结果。

## 分叉与恢复

- 两条合法双签转让从同一父节点派生即构成分叉；域名/字典序排序不参与裁决。
- 分叉未解决前，两支都为 `wait_fork_unresolved`，且冻结一切部分字段发布
  （更新、重托管、别名、删除均不发布），重放传输签名或旧别名都无法解锁。
- 仅 `recovery` 事件可合并：必须引用恰两支不同末端、达到 `create` 时承诺的
  `recoveryPolicy.threshold` 张不同成员投票，且投票对同一恢复主张体密码学有效。
- 完全撤回（`scope: 'all'`）的成员无投票资格；恢复合并取两支撤回集合的并集，
  已撤回的编辑权不会因恢复复活。
- 分叉只隔离冲突对象及其 `parentObject` 后代子树，其他对象正常发布。

## 检查点与历史

- `foldCheckpoint` 压缩已确认前缀，但保留全部同代异摘要 `genDigests`、旧别名与
  撤回集合；压缩后仍能识别同代竞争与历史别名。
- `AuthorizationCertificate` 提供历史授权证书验证，公钥随证书保存，不依赖进程缓存；
  解析本身无缓存，每次从完整信封集合重建，决策回指完整事件哈希链。

## 主要入口

- `resolveOwnership(envelopes, keyResolver, options?)`：完整解析，返回决策、视图、隔离集合。
- `resolveAt(envelopes, keyResolver, asOf, checkpoints?)`：时点查询。
- `buildLookupIndex(resolution)`：URL/别名索引（同 URL 返回多条）。
- `foldCheckpoint` / `signWithCheckpointKey` / `verifyCheckpoint`：检查点压缩与验签。
- `verifyHistoricalAuthorization`：历史授权证书验证。
- `factory.ts`：服务端构造并签名各类事件信封（含双签转让、恢复投票）。

## 判定原因（DecisionReason）

- 等待：`wait_missing_ancestor`、`wait_unknown_key`、`wait_fork_unresolved`、
  `wait_descendant_isolated`、`wait_recovery_threshold`。
- 拒绝：`reject_bad_signature`、`reject_unauthorized_signer`、`reject_transport_only`、
  `reject_stale_gen`、`reject_revoked_editor`、`reject_fully_revoked_member`、
  `reject_already_deleted`、`reject_alias_taken`、`reject_not_a_fork`、
  `reject_recovery_membership`、`reject_canonical_tip`、`reject_malformed` 等。
