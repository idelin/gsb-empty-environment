# 推送认证疲劳与数字匹配会话错绑

登录服务连续发送推送，攻击者希望用户误点一次就批准攻击者会话，或把一个数字匹配结果搬到另一登录。给Go认证宿主实现可导入的推送挑战状态机：挑战绑定accountID、browserSessionID、deviceID、128位challengeID、三位显示码、请求摘要、风险级别、创建与截止单调时刻；显示码由HMAC-SHA256(服务密钥,challengeID||请求摘要)模1000得到，不由客户端指定。同一账户同一时刻最多一个Pending挑战，challengeID和browserSessionID组合必须全局唯一，新挑战只能显式取消旧挑战并进入30秒冷却；每小时创建上限和连续拒绝锁定按服务端原子计数，失败发送也计入但不产生Pending。设备响应须由登记设备Ed25519签名覆盖全部挑战字段和决定{approve,deny}，签名验证后原子从Pending转Approved/Denied，一次性；approve还须回传显示码且设备当前仍属于账户。只有同browserSessionID持有的登录事务可消费Approved签发会话，消费后Completed；超时、设备撤销、密码变更或风险升级使Pending/Approved失效。实现确定调度器交错攻击者和用户两浏览器，搜索待审实现中攻击者会话在用户未批准该请求摘要时获得会话的最短轨迹。覆盖旧挑战迟到approve、码碰撞、取消后回调、并发approve和推送发送失败。

## 实现

包路径为 `example.org/netsec/q0122`，仅依赖 Go 标准库。

```go
srv, err := netsec.NewServer(netsec.Config{
    ServiceKey:           serviceKey,
    HourlyCreationLimit:  10,
    DenialLockThreshold:  5,
})

_ = srv.RegisterDevice(deviceID, accountID, devicePublicKey)
tx, _ := srv.BeginLogin(accountID, browserSessionID)

ch, err := srv.CreateChallenge(netsec.ChallengeRequest{
    AccountID:        accountID,
    BrowserSessionID: browserSessionID,
    DeviceID:         deviceID,
    RequestDigest:    netsec.DigestRequest(loginRequest),
    Risk:             netsec.RiskMedium,
}, func(ch netsec.Challenge) error {
    return pushProvider.Send(ch)
})

// 设备端只对服务端下发的完整绑定签名。
response, _ := netsec.SignApproval(devicePrivateKey, *ch)
approved, err := srv.Respond(response)

// 只有 tx.BrowserSessionID 与挑战绑定一致时才能签发会话。
session, err := srv.ConsumeApproval(tx.ID, approved.ID, approved.DisplayCode)
```

关键约束：

- `ChallengeID` 固定为 16 字节，由服务端随机源生成；`ChallengeRequest` 不包含 ID 或显示码字段。
- 显示码严格执行 `HMAC-SHA256(serviceKey, challengeID || requestDigest) mod 1000`，输出补零到三位。
- 同一账户最多一个 `Pending`；已有 Pending 时新请求返回 `ErrPendingExists`，必须调用 `CancelChallenge`，随后 30 秒冷却期内创建会被 `ErrCooldown` 拒绝。
- 每小时创建限额使用单调时刻的滑动窗口；发送失败在调用推送发送器之前就已计入，失败后状态为 `ChallengeSendFailed` 且不会占用 Pending 槽。
- 连续拒绝计数与锁定在服务端互斥锁内原子更新；批准会清零连续拒绝计数。
- `DeviceResponse.SigningMessage` 覆盖 account、browser session、device、challenge ID、显示码、请求摘要、风险、创建/截止时刻和决定。服务端先比较全部绑定，再验证 Ed25519 签名，并在同一临界区内完成 `Pending -> Approved/Denied`。
- `Approved` 只允许由 `LoginTransaction.BrowserSessionID` 与挑战绑定完全相同的开放事务消费；消费原子地进入 `ChallengeCompleted` / `TransactionConsumed`。
- 超时、设备撤销、密码变更和风险升级都会把对应的 `Pending` / `Approved` 转为 `ChallengeExpired` 或 `ChallengeInvalidated`。

状态机：

```text
Sending --send ok--> Pending --approve--> Approved --consume--> Completed
   |                   |  \--deny-------> Denied
   |                   \--cancel-------> Cancelled
   \--send fail-------> SendFailed
Pending/Approved --timeout--> Expired
Pending/Approved --password/risk/device revoke--> Invalidated
```

## 双浏览器交错搜索

`Auditor` 使用真实状态机实例的深拷贝进行确定性 BFS，交错用户浏览器和攻击者浏览器的创建、取消、批准、拒绝、消费、超时和策略变更动作：

```go
auditor, _ := netsec.NewAuditor(srv, netsec.AuditConfig{
    AccountID:       accountID,
    DeviceID:        deviceID,
    DevicePrivateKey: devicePrivateKey,
    UserBrowser:     "user-browser",
    AttackerBrowser: "attacker-browser",
    UserDigest:      userDigest,
    AttackerDigest:  attackerDigest,
})

result, err := auditor.Search(netsec.SearchOptions{
    MaxDepth:             6,
    MaxNodes:             200000,
    IncludeDenials:       true,
    IncludeAccountEvents: true,
    IncludeSendFailures:  true,
})
```

搜索目标是“攻击者事务在用户没有批准完全相同 challenge/digest/browser 绑定的情况下消费出会话”。正确实现的结果是 `Found == false`；`Complete == true` 表示在给定深度内已穷尽全部交错。`ActionCounts` 可用于确认搜索实际覆盖了批准、攻击者消费、迟到/重复响应和失败发送。

## 测试

```sh
go test ./...
go test -race ./...
go vet ./...
```

测试覆盖：旧挑战迟到批准、三位码碰撞与响应移植、取消后的发送回调、并发批准/消费、推送失败计数、滑动小时窗口、拒绝锁定、超时、设备撤销、密码变更、风险升级和浏览器错绑。
