# hostlock — 同主机多进程互斥锁（租约 + 围栏令牌）

纯标准库实现的同主机多进程互斥协议，带锁所有者记录、可注入时钟的租约、
单调递增的围栏令牌（fencing token）和故障注入点。

## 文件布局（全部位于调用方指定目录）

```
<dir>/<name>.mutex   串行化文件：flock(LOCK_EX) 保护所有写操作，本身无数据
<dir>/<name>.owner   JSON 所有者记录：tmp 写入 + fsync + rename 原子替换
<dir>/<name>.token   已发放的最大围栏令牌，永不删除，保证跨崩溃/重启单调
```

所有者记录包含：pid、主机名、**每次获取随机生成的 nonce**、进程启动签名
（Linux 下为 boot_id + /proc 启动时间）、围栏令牌、获取时间与租约到期时间。

## 协议要点

- **互斥**：所有写操作（获取/接管/续约/释放）先对 `.mutex` 文件取 `flock`。
  flock 由内核在进程死亡时自动释放，因此崩溃不会造成元数据死锁。
- **租约**：获取/续约写入 `ExpiresAt = clock.Now() + Lease`。时钟通过
  `Clock` 接口注入（`RealClock` / `FakeClock`），测试可确定性推进时间。
- **四种状态**（`Inspect`）：`free`（无记录）、`live`（租约有效且进程存活）、
  `lost`（租约有效但进程已消失——失联，他人必须等租约到期）、
  `stale`（租约已过期——陈旧，可被接管）。
- **围栏令牌**：每次成功获取（含接管陈旧锁）把 `.token` 中的计数 +1 并
  fsync。下游资源用 `Checker`（内存）或 `FileFence`（跨进程持久化）只接受
  严格更大的令牌，因此**过期后恢复的旧持有者无法凭旧令牌提交**。
- **nonce 所有权**：`Release`/`Renew` 必须出示与记录匹配的 nonce。nonce 只
  存在于持有者内存的 `Handle` 中，其他进程（包括复用了同一 pid 的进程）
  无法伪造，**释放别人的锁会被拒绝**（`ErrNotOwner`），且不会误删记录。
- **pid 重用**：三重防护——(1) 正确性不依赖 pid：租约 + nonce + 令牌决定
  一切；(2) Linux 下记录进程启动签名，pid 相同但签名不同则判为 `lost`；
  (3) 其他平台签名不可用时仅影响 live/lost 的展示，不影响所有权安全。
- **故障注入点**（`Hooks`，生产为 nil）：
  - `FailBeforeOwnerWrite`：令牌已持久化、所有者记录未写之间崩溃；
  - `FailBeforeFsync`：任意 fsync 失败（模拟断电/磁盘故障）。

## 崩溃恢复

持有者在任意时刻崩溃：

1. `.owner` 记录残留；租约未到期时他人看到 `lost`，`TryAcquire` 返回
   `*HeldError`（含所有者信息），必须等待——防止脑裂期间双写。
2. 租约到期后状态变为 `stale`，任一客户端在 flock 下接管：令牌 +1、
   写入新 nonce 的新记录。
3. 旧持有者若"复活"：其 `Release`/`Renew` 因 nonce 不匹配被拒；其旧令牌
   的下游提交因令牌不递增被拒。旧持有者绝不可能再影响锁或数据。
4. 若崩溃发生在"令牌已写、记录未写"之间（注入点可复现）：锁表现为空闲，
   但令牌不回退，下一次获取拿到更大的令牌——宁可浪费令牌，绝不重用。

## 断电保证的边界

**保证**（在存储栈诚实执行 fsync 的前提下）：

- 所有状态变更顺序为：写临时文件 → fsync 文件 → rename → fsync 目录。
  `TryAcquire`/`Renew`/`Release` 返回成功时，对应记录已持久化。
- rename 原子性保证观察者只会看到完整的新或旧记录，不会读到撕裂内容。
- 令牌文件一旦 fsync 即单调不回退；断电后重启，令牌序列继续递增。
- 断电后残留的锁最迟在租约到期后被接管（租约用墙钟，重启后自然过期）。

**不保证**（明确的边界）：

- 磁盘/RAID 控制器虚报 fsync（缓存在掉电时丢失）：可能丢失最后的记录或
  令牌回退。这是所有本地文件锁的共同边界，需关掉写缓存或用带掉电保护
  的硬件。
- 墙钟被管理员/NTP 大幅回拨：租约到期判断基于墙钟，回拨会延长有效租约
  （安全方向）；向前跳变会提前过期（此时围栏令牌仍阻止旧持有者提交，
  互斥由令牌兜底）。
- 跨主机：flock 与租约只在单机有效，NFS 等场景不在保证范围内。
- 非 Linux 平台无进程启动签名：`live`/`lost` 状态展示可能把复用 pid 的
  无关进程误标为 live，但所有权与 fencing 安全不受影响（nonce + 令牌）。

## 运行样例

```
$ go run ./examples/demo
lock dir: /tmp/hostlock-demo (lease 1.2s)

== 1. worker process acquires the lock, commits, then crashes without releasing
   [child] token=1 pid=9606
   [child] exited with code 2 (crash)

== 2. parent inspects: lease still valid but owner pid is dead -> lost
   status=lost owner={... PID:9606 ... Token:1 ...}
   TryAcquire while lost: hostlock: lock is held (pid 9606 ..., token 1, ...)

== 3. parent waits out the lease, then takes over the stale lock
   status after expiry: stale
   acquired with fencing token 2 (was 1)
   commit with token 2: accepted

== 4. crashed worker 'wakes up' and replays its old token
   [child] zombie commit with old token 1: REJECTED (hostlock: stale fencing token: token 1, already fenced at 2)

== 5. old handle cannot release or commit after takeover
   new generation acquired with token 3
   old handle Release: hostlock: not the lock owner
   old token 2 commit: hostlock: stale fencing token: token 2, already fenced at 3

final status: free
```

## 测试结果

```
$ go test -count=1 -v ./...
--- PASS: TestAcquireInspectRelease (0.07s)
--- PASS: TestContentionReturnsHeld (0.05s)
--- PASS: TestStaleTakeoverAndOldHolderRejected (0.48s)
--- PASS: TestOldTokenCannotCommit (0.07s)
--- PASS: TestLostThenStaleStatus (0.03s)
--- PASS: TestPidReuseNotMisidentified (0.03s)
--- PASS: TestReleaseOthersLockRejected (0.13s)
--- PASS: TestRenewExtendsAndRefusesAfterExpiry (0.16s)
--- PASS: TestFaultInjectionCrashBetweenTokenAndOwner (0.07s)
--- PASS: TestFaultInjectionFsyncFailure (0.00s)
--- PASS: TestTokenMonotonicAcrossReleaseAndClients (0.09s)
--- PASS: TestConcurrentTryAcquire (0.98s)
--- PASS: TestInvalidOptions (0.00s)
--- PASS: TestHelperProcess (0.00s)
--- PASS: TestCrossProcessCrashRecovery (0.99s)   # 真实子进程崩溃恢复
PASS
ok  	hostlock	3.261s
```

其中 `TestCrossProcessCrashRecovery` 用 `os/exec` 重启测试二进制作为子进程，
真实获取锁后以退出码 2 "崩溃"（不释放），父进程观察到 lost → stale → 接管
且令牌单调递增。并发测试以 8 goroutine × 50 次竞争验证无双重持有。
