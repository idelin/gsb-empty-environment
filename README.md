# 租约领导 + 单调 fencing token（单进程事件模拟）

仅用 Go 标准库实现。通信、故障（暂停/崩溃/丢包/链路延迟）与时间（虚拟参考时钟 + 每节点显式时钟偏差）全部由 `sim` 包的单进程事件模拟器控制，无 goroutine、无真实时钟依赖，测试可确定性复现。

## 结构

- `sim/`：事件模拟器（优先队列按虚拟时间推进）、`Pause/Resume/Crash/Recover/Drop/SetDelay` 故障与网络控制、`SkewedClock` 显式时钟偏差模型（固定偏移 + 可选漂移）。
- `lease/coordinator.go`：租约权威与 fencing token 分配器。同一时刻只授予一个未过期租约；`term` 与 `fence` 只增不减；只有持有效租约者能换新 token。
- `lease/node.go`：节点状态机（follower/candidate/leader）。节点用**本地时钟**判断租约，并把本地截止点设为 `expiresAt - 2*maxClockError`，即在参考租约到期前提前让位——租约只是活性优化。
- `lease/resource.go`：资源端唯一的安全裁决者。只接受严格递增的 fence；`fence <= lastFence`（重复或回退）一律拒绝，不看租约、不看任何时钟。
- `cmd/leasedemo/`：可运行演示。

## 安全性证据（ fencing，而非租约隔离）

不变式：资源端已接受的最大 fence 为 `F` 时，任何 `fence <= F` 的写都不会改变资源状态。

1. token 由协调器单调分配（`fence++`），且只有当前租约持有者能获取，因此旧领导能拿到的最大 fence 必然小于新领导之后拿到的 fence。
2. 资源接受写当且仅当 `fence > lastFence`，接受后 `lastFence` 单调不减（归纳不变式）。
3. 因此旧领导暂停后恢复/补发的写（fence 较小）必被拒绝；新领导写成功后，旧消息无法覆盖；`fence == lastFence`（重复）与 `fence < lastFence`（回退）均非法。
4. 时钟偏差只影响节点**何时主动让位**（提前 `2*maxClockError`），从不作为资源接受写的依据——租约本身不被当作强隔离。

对应测试：`TestOldLeaderStaleWriteCannotOverwriteNewLeader`、`TestResourceRejectsDuplicateAndRollback`、`TestClockSkewModelForcesEarlyStepDown`。

## 活性证据

在“协调器可用、候选者与协调器间消息最终可达、TTL > 2*maxClockError”的假设下：

1. 旧领导崩溃/暂停后，协调器在参考时间 `expiresAt` 后必然接受新 campaign（授予条件只检查参考时钟下的租约是否过期）。
2. 候选者每 `TTL/4` 重试 campaign，丢包恢复后有限步内必获租约。
3. 新领导获租约后即可换 fence 并写资源，`fence > lastFence` 必被接受。

对应测试：`TestNewLeaderAcquiresLeaseAndWritesAfterExpiry`（A 崩溃 + B 首次 campaign 丢包，重试后成为领导并写成功）。

## 运行样例

```console
$ go run ./cmd/leasedemo
t=00:00:00.000 显式时钟偏差: A=200ms B=-200ms (maxClockError=250ms)
t=00:00:00.100 A 成为领导 term=1
t=00:00:00.150 A 取得 fence=1 并发出写（链路延迟 5s）
t=00:00:00.150 A 暂停（GC/调度停顿），租约继续到期
t=00:00:04.450 B 成为新领导 term=2
t=00:00:04.650 资源接受 B: fence=2 value="v2-from-B"
t=00:00:06.150 A 恢复后角色=follower；A 的旧写到资源被拒绝: stale fence token
最终: value="v2-from-B" lastFence=2 rejected=1
```

## 测试结果

```console
$ go test -v ./...
?   	leasefencing/cmd/leasedemo	[no test files]
=== RUN   TestOldLeaderStaleWriteCannotOverwriteNewLeader
--- PASS: TestOldLeaderStaleWriteCannotOverwriteNewLeader (0.00s)
=== RUN   TestResourceRejectsDuplicateAndRollback
--- PASS: TestResourceRejectsDuplicateAndRollback (0.00s)
=== RUN   TestClockSkewModelForcesEarlyStepDown
--- PASS: TestClockSkewModelForcesEarlyStepDown (0.00s)
=== RUN   TestNewLeaderAcquiresLeaseAndWritesAfterExpiry
--- PASS: TestNewLeaderAcquiresLeaseAndWritesAfterExpiry (0.00s)
PASS
ok  	leasefencing/lease	2.860s
?   	leasefencing/sim	[no test files]
```

注：本机 Go 1.11 需 `GO111MODULE=on` 并指定可写的 `GOCACHE`；Go 1.16+ 直接 `go test ./...` 即可。
