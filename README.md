# sessionkv — 异步复制系统中的会话版本令牌

纯 Go 标准库实现。通信、故障与时间全部在单进程离散事件模拟器中受控；
不依赖真实时钟与网络，不强迫全局同步，不承诺全局线性一致。

## 设计

- **版本向量**（`vector.go`）：跨键因果版本向量 `节点ID -> 序号`，
  支持 `Merge` / `Covers`（逐分量偏序）。
- **会话令牌**（`token.go`）：`Token{SessionID, Vector}`，绑定会话，
  携带该会话已观察到的因果前沿。令牌随会话迁移（`Client.Migrate`），
  不同会话的令牌混用会被 `Check` 显式拒绝。
- **节点**（`node.go`）：
  - 写：仅当节点因果前沿覆盖写的依赖时才应用，否则等待复制补齐；
    写确认携带完整因果版本，并入会话令牌。
  - 读：仅当节点因果前沿**覆盖令牌向量**时才返回快照——因此绝不会
    返回"满足本键版本却缺少其依赖"的快照；否则挂起等待，超时显式失败。
  - 复制：异步泛洪，消息携带 `Deps`，接收方因果缓冲、按依赖顺序投递。
  - 崩溃/重启：状态持久化保留；重启后反熵拉取对等方状态，令牌继续有效。
  - 快照：`InstallSnapshot` 合入（只前进）；`ResetSnapshot` 模拟回退式
    安装，令牌要求超出新前沿的读只能等待或显式超时，禁止静默降级。
- **模拟器**（`simulator.go`）：虚拟时间事件队列 + 可配置链路
  （延迟/分区），所有通信与故障注入均由此调度。

## 安全性证据（测试即证明）

| 性质 | 测试 |
|---|---|
| 写在A成功后转B读取不返回旧值（读己之写+会话迁移） | `TestReadYourWritesAfterMigration` |
| 超时显式标记未满足，不静默返回旧值 | `TestReadTimeoutMarkedUnsatisfied` |
| 单调读（同会话读版本不回退） | `TestMonotonicReads` |
| 跨键因果：缺依赖的快照不对读可见 | `TestCrossKeyCausalDependency` |
| 不同会话令牌不混用 | `TestTokenSessionIsolation` |
| 重启后令牌继续有效 | `TestRestartKeepsTokenValid` |
| 快照安装后令牌可满足 | `TestSnapshotInstallSatisfiesToken` |
| 回退式快照后禁止静默降级 | `TestRegressiveSnapshotNoSilentDegrade` |
| 随机迁移场景下独立检查 RYW + 单调读 | `TestRandomizedConsistencyChecker` |

独立检查器不复用客户端令牌逻辑，只依据会话的写确认与读结果记录，
逐条验证：读值必为本会话某次已确认写、读版本等于该写确认、同键连续读
版本单调。

## 活性证据

`TestRandomizedConsistencyChecker` 在 3 节点、有界随机延迟（1–8）、
4 个迁移会话 × 30 次操作的有限消息模拟中，全部 240 次读写均在期限内
完成、零超时——在链路有界可达的前提下，等待中的读最终会被满足。
活性以"覆盖令牌或显式超时"为界，不以全局同步为前提。

## 运行

```sh
go test ./... -v   # 全部安全性/活性测试
go run ./cmd/demo  # 三个场景的跟踪输出
```

## 运行样例（`go run ./cmd/demo`）

```
== 场景1: 写在A成功后迁移到B读取（读己之写，禁止返回旧值）==
[t=  0] 写 profile=v0-old @B 确认, 令牌={B:1}
[t=  0] 写 profile=v1-new @A 已提交（等待依赖）
[t= 15] 写 v1-new @A 确认, 令牌={A:1 B:1}
[t= 15] 会话 alice 迁移到节点 B
[t= 15] 在 B 发起读（B 当前只有旧值，读挂起等待复制）
[t= 25] 读完成: value="v1-new" version={A:1 B:1} (复制到达后返回新值)

== 场景2: 网络分区下读超时（显式标记未满足，不静默降级）==
[t=  0] 网络分区: A -/-> B
[t=  0] 写 config=fresh @A 确认, 令牌={A:1}
[t=  0] 在 B 读 config（B 有旧值 stale，但不满足令牌，等待）
[t= 60] 读超时: err=sessionkv: consistency requirement not satisfied before timeout: read key="config" required={A:1}

== 场景3: 节点重启后令牌继续有效（反熵补齐，不降级）==
[t=  0] 写 doc=v1 @A 确认, 令牌={A:1}
[t=  1] 节点 B 崩溃
[t=  2] 在 B 读 doc（B 宕机，读挂起）
[t=  5] 节点 B 重启，发起反熵
[t= 40] 读完成: value="v1" version={A:1}（重启后令牌仍被满足）
```

## 测试结果（`go test ./... -v`）

```
--- PASS: TestReadYourWritesAfterMigration (0.00s)
--- PASS: TestReadTimeoutMarkedUnsatisfied (0.00s)
--- PASS: TestMonotonicReads (0.00s)
--- PASS: TestCrossKeyCausalDependency (0.00s)
--- PASS: TestTokenSessionIsolation (0.00s)
--- PASS: TestRestartKeepsTokenValid (0.00s)
--- PASS: TestSnapshotInstallSatisfiesToken (0.00s)
--- PASS: TestRegressiveSnapshotNoSilentDegrade (0.00s)
--- PASS: TestRandomizedConsistencyChecker (0.00s)
    checked 120 writes and 120 reads across 4 migrating sessions:
    RYW + monotonic reads hold, no timeouts
PASS
ok  	sessionkv
```
