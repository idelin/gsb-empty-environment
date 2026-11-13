# 社交恢复联系人串谋与代次复用

账户允许多个恢复联系人达到阈值后重置主凭据，被移除联系人或同一人控制的多个别名不能继续凑数。给Go身份宿主实现可导入的社交恢复包：账户配置含不可变recoveryEpoch、联系人条目(contactID、公钥指纹、唯一personID、权重、启止epoch)、阈值、24小时冷却和最低不同personID数量；同personID的所有条目总共最多计其最大单项权重，公钥材料相同的别名同样合并。开始恢复生成随机256位recoveryID，绑定accountID、epoch、目标操作摘要、发起设备摘要与截止时间；联系人批准为Ed25519签名，覆盖全部字段和自己的contactID。只计当前epoch有效、未撤销、签名正确且personID/公钥去重后的权重；达到阈值先进入Cooling，不立即改凭据。冷却结束前，原账户任一未撤销强因子可否决；配置变化、联系人撤销、目标操作变化或新恢复开始均递增epoch并使旧批准失效。完成时原子轮换主凭据、撤销全部会话和恢复ID，再递增epoch；崩溃恢复不得重复完成。实现待审去重/epoch回调的差分搜索，找正确模型拒绝而待审完成恢复的最小批准集。覆盖同人多联系人、换公钥、阈值降低、冷却末刻否决、旧epoch签名和完成写盘故障。

## Package API

The package is `example.org/netsec/q0123` with package name `netsec`.

- `Host` owns `CreateAccount`, `UpdateConfig`, `RemoveContact`, `RevokeContact`,
  `RevokeStrongFactor`, `StartRecovery`, `Approve`, `Veto`, `ChangeTarget`,
  `EvaluateRecovery`, and `CompleteRecovery`.
- `Config` is an immutable epoch snapshot. Mutating operations create a new
  snapshot and increment `RecoveryEpoch`; an in-flight recovery from the old
  epoch is invalidated.
- `Contact` carries `ContactID`, `PersonID`, an Ed25519 `PublicKey`, its
  `Fingerprint`, `Weight`, and an inclusive/exclusive `[StartEpoch, EndEpoch)`
  validity window (`EndEpoch == 0` means open-ended).
- `SignApproval` and `SignVeto` produce signatures over the package's
  domain-separated canonical byte encoding. Approval signatures cover the
  account, recovery ID, epoch, target digest, device digest, deadline,
  contact ID, and key fingerprint.
- `Store` is the atomic persistence boundary. A commit replaces one account
  state only when the expected version matches, which keeps credential
  rotation, session revocation, the completion marker, and the post-completion
  epoch bump in one durable write.
- `FindMinimalDivergence` performs the differential approval-subset search
  against `CorrectPolicy` and a pending policy such as
  `PendingDedupEpochPolicy`.

`MemoryStore` provides failure injection for crash tests:
`FailNextCommit` fails before persisting, while
`FailNextCommitAfterPersist` persists the atomic state and then returns an
error, modelling a lost response after a successful write.
