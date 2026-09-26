# 会话版本令牌（异步复制，单进程事件模拟）

仅用 Go 标准库实现。通信、故障与时间全部由单进程离散事件模拟器
（`sim.go`）控制；不强迫全局同步，复制完全异步。

## 设计

- **版本向量**（`vv.go`）：`VersionVector` 为 节点到序号 的因果时钟，
  支持 `Merge` 与 `Dominates`。
- **会话令牌**（`token.go`）：`Token` 是跨键因果版本向量集合
  （key 到所需最小版本）。读请求只有在副本对令牌中每一个键的版本
  都被满足时才返回，因此不会返回本键版本够、却缺少因果依赖的快照。
- **节点**（`node.go`）：写请求本地定序、持久化到 `Disk`、确认后异步复制；
  读请求不满足令牌则挂起，复制或快照到达时重新检查，超时返回
  `ErrConsistencyTimeout`，显式标记一致性未满足，绝不返回旧值。
- **会话**（`session.go`）：写确认与读结果都会并入令牌（读己之写、单调读
  的基础）；`Migrate()` 让令牌随会话迁移到其他客户端侧或节点。
- **重启与快照**：崩溃后从 `Disk` 恢复，版本不丢，令牌继续有效；
  `InstallSnapshot` 逐键合并、版本只升不降，过期快照被显式跳过，
  禁止静默降级。无法满足时只能以显式错误暴露。
- **令牌不混用**：令牌绑定 `SessionID`，节点校验请求会话与令牌归属，
  不一致返回 `ErrTokenMismatch`。
- **不承诺全局线性一致**：无令牌约束的会话允许读到旧值
  （见 `TestNoGlobalLinearizabilityPromise`）。

## 证据

- **安全性**：`TestEnumerationSafetyAndLiveness` 在有限消息模拟中枚举
  会话迁移、写确认与读超时的全部 16 条轨迹（读目标、丢包掩码、超时），
  断言每条轨迹要么返回新值、要么显式超时，绝不返回旧值；
  `TestMonotonicReads`、`TestCrossKeyDependency`、快照与重启测试
  分别独立检查单调读、跨键依赖与禁止降级。
- **活性**：`TestReadYourWritesAfterMigration` 证明复制到达前挂起的读
  最终完成；枚举测试证明超时机制必然触发（12 条轨迹显式超时、4 条成功）。

## 运行

```sh
go test ./... -v   # 全部测试
go run ./cmd/demo  # 运行样例
```

### 测试结果

```
--- PASS: TestWriteAckUpdatesToken
--- PASS: TestReadYourWritesAfterMigration
--- PASS: TestReadTimeoutMarkedUnmet
--- PASS: TestCrossKeyDependency
--- PASS: TestMonotonicReads
--- PASS: TestRestartKeepsTokenValid
--- PASS: TestSnapshotInstallNoDowngrade
--- PASS: TestTokenMixupRejected
--- PASS: TestEnumerationSafetyAndLiveness
    simulation_test.go:272: enumerated 16 traces: 4 succeeded, 12 explicit consistency timeouts
--- PASS: TestNoGlobalLinearizabilityPromise
PASS
ok  	sessiontoken	0.498s
```

### 运行样例（go run ./cmd/demo）

```
== 1. 会话 s1 在 A 写入 k=v1（写确认携带版本） ==
    ack ver={A:1}, token={A:1}
    [t=   0] A: write k="v1" ver={A:1} ack->s1
    [t=   0] A->B: replicate k ver={A:1} delay=10
    [t=   0] A->C: replicate k ver={A:1} delay=10
== 2. 会话迁移到 B 读取：等待复制到达后返回新值（读己之写） ==
    read(B) -> "v1", err=<nil> (t=10)
    [t=   0] B: read k by s1 PENDING (token not satisfied, timeout=50)
    [t=  10] B: applied replicate k from A, ver={A:1}
    [t=  10] B: pending read k now satisfied -> "v1" ver={A:1}
== 3. 故障注入丢弃 A->C 复制：读超时显式标记一致性未满足 ==
    read(C) -> "", err=consistency timeout: token requirements not met
    [t=  10] C: read k2 by s2 PENDING (token not satisfied, timeout=30)
    [t=  40] C: read k2 by s2 TIMEOUT: consistency not met
== 4. A 崩溃并重启：令牌继续有效，不静默降级 ==
    read(A) after restart -> "v1", err=<nil>
    [t=  40] A: CRASH
    [t=  40] A: restart, restored 2 keys from disk
    [t=  40] A: read k -> "v1" ver={A:1} (token satisfied)
== 5. 令牌混用被拒绝 ==
    s3 using s1's token -> err=session token mismatch
DEMO OK
```
