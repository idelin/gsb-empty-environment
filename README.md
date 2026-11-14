# 设备准入与范围同步组件

供 Go 凭据保管服务导入的准入策略组件。它不保存账户密钥明文，只保存
调用方提交的密文条目和面向每台设备的账户密钥封装。

## 安全模型

- 新设备调用 `BeginEnrollment` 后处于 `pending`，不能同步、不能读取
  wrapped key，也不能自行批准。
- 已批准设备调用 `ApproveEnrollment`（或显式全量授权的
  `ApproveEnrollmentAll`）后才生效。批准时必须提交由 X25519 + HKDF +
  AES-256-GCM 生成的账户密钥封装，封装上下文绑定账户、设备和准入请求。
- 恢复码只存 HMAC-SHA256 摘要，具有 160 位随机熵；消费和待批准请求的
  创建在同一把锁内完成，因此并发复用也只有一个请求能成功。
- 恢复码成功使用会记录 `AlertRecoveryCodeUsed`；复用会记录
  `AlertRecoveryCodeReuse`。恢复码本身不授予任何访问，仍需现有设备批准
  和密钥封装。
- `Sync` / `SyncAll` 先校验账户归属，再校验设备状态和条目范围。越权条目
  不会被部分返回，不会暴露条目是否存在。
- `RevokeDevice` 清除封装密钥和条目范围，之后所有同步、写操作和密钥
  读取立即返回 `ErrDeviceRevoked`。

托管服务必须先从已认证的传输会话获得设备身份，再把 `DeviceID` 传给本
组件；组件不替代 TLS/设备签名等传输身份认证。

`Service` 是内存态组件，零值可直接使用。托管服务负责把设备/条目记录和
告警持久化到自己的数据库；进程重启后应恢复状态，或重新走准入流程。

## 基本用法

```go
svc := netsec.NewService()

owner, _ := netsec.GenerateDevice("owner-phone")
accountID, _ := svc.CreateAccount(owner.Public())

joining, _ := netsec.GenerateDevice("new-laptop")
enrollment, _ := svc.BeginEnrollment(accountID, joining.Public())

binding := netsec.DeviceKeyBinding(accountID, joining.ID, enrollment.ID)
wrapped, _ := netsec.WrapAccountKey(accountKey, joining.Public().KeyAgreementPublicKey, binding)
_ = svc.ApproveEnrollment(accountID, owner.ID, enrollment.ID, wrapped, []netsec.EntryID{"mail"})

entries, _ := svc.Sync(accountID, joining.ID, []netsec.EntryID{"mail"})
```

`accountKey` 由账户侧生成并只在设备间封装传递；服务端只见到 `wrapped`。
新设备用自己的私钥和相同的 `binding` 调用 `UnwrapAccountKey`。

## 验证

`go test -race ./...` 覆盖：合法加入与密钥解封、恢复码复用及并发单次
消费、未批准设备、跨账户同步、条目范围、包装上下文/篡改校验，以及
撤销后立即拒绝同步。

团队凭据保管服务允许新设备加入账户，攻击者用恢复码加入后同步全部条目，取得所有口令。请实现供Go凭据保管服务调用的可导入设备准入组件。新设备加入需现有设备批准与账户密钥封装，恢复码使用一次并告警；同步按设备与条目范围授权。设备撤销立即停止同步。用恢复码复用、未批准设备、跨账户同步、合法加入和撤销验证条目不被窃取。
