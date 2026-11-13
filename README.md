# 恢复方式变更在延迟期提前生效

攻击者拿到当前会话后添加自己的邮箱或设备作为恢复方式，若立即生效就能绕过原强因子接管账户。为Go账户宿主实现恢复方式变更状态机：方法有Email、DeviceKey、RecoveryContact，条目含methodID、验证材料摘要、创建代次、状态Pending/Active/Revoked和生效单调时刻。新增或替换请求绑定accountID、当前配置摘要、请求会话ID、目标方法和随机requestID；高风险方法必须由两个不同methodID且属于不同类别的当前Active强因子确认，低风险至少一个，当前登录会话本身不算确认。所有确认签名覆盖请求完整字段；达到确认后进入Pending并等待调用方策略给定延迟，延迟内旧Active方法保持，任何旧强因子可取消。Pending不能用于登录、恢复或确认其他变更。生效时重新核对确认因子仍Active、账户配置除该Pending外未变且会话未因密码重置撤销，然后原子激活并按请求撤销被替换方法；配置代次递增使其他Pending全部失效。删除最后一个强因子拒绝。实现两个并发变更的调度搜索，找待审实现中Pending方法提前完成恢复或用Pending互相确认的最短轨迹。覆盖延迟边界、确认因子中途撤销、替换同methodID、密码重置和写盘故障。

## Implementation

The package exposes an `Engine` state machine over a pluggable `Store`.
Entries are versioned (`Entry.ID`) so replacing a method with the same public
`MethodID` cannot accidentally revoke the replacement.

```go
clock := netsec.NewManualClock(1)
verifier := netsec.HMACVerifier{Secret: []byte("deployment-secret")}
engine := netsec.NewEngine(netsec.NewMemoryStore(),
	netsec.WithClock(clock),
	netsec.WithVerifier(verifier),
	netsec.WithPolicy(netsec.DefaultPolicy()),
)
```

`DefaultPolicy` treats every built-in method as high risk and strong. Deployers
that classify a narrower channel as low risk can override `Policy.Risk`
without weakening the two-category rule for high-risk changes.

Typical flow:

1. `CreateAccount` bootstraps trusted entries.
2. `OpenSession` records an authenticated session.
3. `NewChangeRequest` fills the current config digest and a random request ID.
4. `HMACVerifier.Sign` (or another `SignatureVerifier`) signs the returned
   request; `Propose` verifies and records a Pending entry.
5. `Activate` at or after the delay boundary rechecks everything and commits
   atomically. `Cancel` lets any active strong factor revoke the Pending change.
6. `ResetPassword` increments the session epoch, so old sessions cannot carry a
   Pending change through activation.

`FileStore` uses fsync plus atomic rename. The Engine mutates a clone and only
publishes it after `Store.Save` succeeds, so a save failure leaves the previous
account state intact.

`FindShortestUnsafeTrajectory` exhaustively schedules two concurrent changes.
With the safe model it returns `ErrNoAbuseTrace`; `AuditFlaws` can model a
candidate implementation that lets Pending methods authenticate, confirm each
other, or activate early, and the search returns the shortest counterexample.

Run the tests with:

```sh
go test ./...
go test -race ./...
```
