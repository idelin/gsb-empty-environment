# hostmutex — 同主机多进程互斥协议

仅使用 Go 标准库实现的同主机多进程互斥锁，带锁所有者记录、租约、
可注入时钟与单调递增隔离令牌（fencing token）。所有状态文件都放在
调用方指定的目录中，故障通过注入点模拟。

## 目录结构

- `mutex/` — 核心库
  - `mutex.go` — Acquire / Renew / Release / ValidateToken / Inspect，原子提交与故障注入点
  - `record.go` — 锁所有者记录（令牌、OwnerID、PID、进程启动标识、租约到期时刻）
  - `state.go` — 状态分类：free / live / lost（失联）/ stale（陈旧）
  - `clock.go` — 可注入时钟（生产 realClock，测试 FakeClock）
  - `proc.go` — 可注入进程探针（kill(pid,0) + ps 启动时间）
  - `mutex_test.go` — 全部测试（go test）
- `cmd/demo/` — 可运行演示（真实子进程 + SIGKILL 崩溃模拟）

## 协议设计

锁状态持久化为 `<dir>/<name>.lock.json`；元数据的读-改-写通过
`<dir>/<name>.sync` 上的 `flock(LOCK_EX)` 串行化（协议内部的 CAS 原语，
仅持有微秒级）。客户端可见的互斥语义由租约层提供：

1. **Acquire**：在 flock 保护下读取记录并分类：
   - `live`（持有者进程存活、启动标识匹配、租约未过期）→ 返回 `ErrLockHeld`；
   - `free` / `lost`（进程在但租约过期，失联）/ `stale`（进程死亡或 PID 复用）
     → 接管，令牌 = 旧令牌 + 1，写入自己的记录。
2. **Renew**：校验 OwnerID + 令牌 + 租约未过期后延长到期时刻。
3. **Release**：校验 OwnerID，不匹配返回 `ErrNotOwner`（不能释放别人的锁）；
   释放写入"空记录"但**保留令牌**，保证令牌在锁的整个生命周期内单调递增。
4. **ValidateToken**：下游资源的隔离栅栏——令牌不等于当前记录或租约过期即拒绝。

每次写记录采用 `临时文件 + fsync(文件) + rename + fsync(目录)`，
保证掉电后记录要么旧要么新，不会出现半写状态。

### 三种持有者状态的区分

| 状态 | 判定 | 含义 |
| --- | --- | --- |
| live | 进程存活 ∧ 启动标识匹配 ∧ 租约未过期 | 正常持有，他人获取被拒 |
| lost（失联） | 进程存活 ∧ 租约过期 | 可能挂起/时钟异常，允许接管 |
| stale（陈旧） | 进程死亡，或 PID 被复用（启动标识不匹配） | 持有者已消失，允许接管 |

PID 复用的识别：记录中保存持有者进程的启动时间（`ps -o lstart=`，
作为不透明 token 比较）。PID 相同但启动时间不同 ⇒ 不是原持有者。

## 崩溃恢复与断电保证的边界

**保证的部分**

- 进程崩溃（含 SIGKILL）：记录仍在盘上，下一个获取者看到 stale 并接管，
  令牌 +1。崩溃的旧持有者若"醒来"，其 `Renew`/`Release` 因 OwnerID 不匹配
  被拒，凭旧令牌向 `ValidateToken` 提交也被拒（旧令牌 < 当前令牌）。
- 失联（进程活着但停止续约）：租约过期后允许接管；旧持有者被令牌栅栏挡住，
  这是防止脑切的关键——**持有锁不等于有权提交，提交前必须过令牌校验**。
- 掉电：每次状态变更在返回前完成 `fsync(文件) + rename + fsync(目录)`，
  因此 `Acquire` 成功返回后记录已持久化；恢复后要么看到完整旧记录，
  要么看到完整新记录，临时文件不干扰判定。
- 提交结果未知的窗口（`AfterCommit` 注入点模拟）：记录已落盘但调用方
  收到错误；同一句柄重新 `Acquire` 时发现记录是自己的，幂等恢复，
  不会重复递增令牌。

**边界（不保证的部分）**

- `fsync` 的真实性依赖硬件/操作系统（某些磁盘会虚报刷盘完成）；
  文件系统级损坏、目录被整体删除不在保证范围内。
- 租约比较使用墙上时钟（跨进程必须如此）：NTP 大幅回拨/跳变可能让
  租约提前或延后过期。即使误判导致两个进程都自认为持有锁，
  下游的令牌栅栏仍保证只有一个能提交——**令牌是最终防线**。
- 进程启动时间取自 `ps`，精度为秒：同一秒内 PID 复用且启动时间
  字符串恰好相同的极端情形无法区分；`ps` 不可用（token 为空）时
  按"存活"保守处理，宁可不抢占也不破坏互斥。
- 被杀死的子进程在父进程 `wait(2)` 回收前是僵尸，`kill(pid,0)` 仍
  成功；本实现对僵尸按存活处理，回收后自然转为 stale。
- 协议只覆盖同一台主机（flock、PID、/dev 语义均为主机本地）。

## 运行样例

```console
$ go run ./cmd/demo -dir /tmp/mutexdemo
== 1. 启动子进程持锁（pid 待分配）
   子进程 pid=7732 持锁，隔离令牌=1
== 2. 父进程尝试获取 → 应被拒绝（live）
   获取失败（符合预期）: mutex: lock is held by a live owner: pid=7732 token=1
   状态=live 持有者 pid=7732 host=MacBook-Pro-DL
== 3. SIGKILL 子进程，模拟崩溃
   崩溃后状态=stale（stale，可安全接管）
== 4. 父进程接管锁
   接管成功，新隔离令牌=2（旧=1，严格递增）
== 5. 崩溃的旧持有者若凭旧令牌提交 → 必须拒绝
   旧令牌 1 被拒绝（符合预期）: mutex: fencing token is stale: got 1, current 2
   当前令牌 2 校验通过
== 6. 父进程释放锁
   释放后状态=free
演示完成。锁文件位于: /tmp/mutexdemo
```

## 测试结果

```console
$ go test ./mutex/ -v
=== RUN   TestAcquireReleaseAndContention
--- PASS: TestAcquireReleaseAndContention (0.06s)
=== RUN   TestLeaseExpiryTakeoverAndStaleToken
--- PASS: TestLeaseExpiryTakeoverAndStaleToken (0.03s)
=== RUN   TestStaleOwnerProcessDead
--- PASS: TestStaleOwnerProcessDead (0.04s)
=== RUN   TestPIDReuseNotConfusedWithOriginalOwner
--- PASS: TestPIDReuseNotConfusedWithOriginalOwner (0.04s)
=== RUN   TestReleaseOthersLockRejected
--- PASS: TestReleaseOthersLockRejected (0.01s)
=== RUN   TestFencingTokenMonotonic
--- PASS: TestFencingTokenMonotonic (0.16s)
=== RUN   TestFaultInjectionBeforeCommit
--- PASS: TestFaultInjectionBeforeCommit (0.00s)
=== RUN   TestFaultInjectionAfterCommit
--- PASS: TestFaultInjectionAfterCommit (0.01s)
=== RUN   TestConcurrentAcquireSingleWinner
--- PASS: TestConcurrentAcquireSingleWinner (0.03s)
=== RUN   TestCrossProcessCrashRecovery
--- PASS: TestCrossProcessCrashRecovery (0.14s)
PASS
ok  	hostmutex/mutex	0.689s
```

测试覆盖：基本互斥与竞争、租约过期失联接管、旧令牌/旧持有者续约与释放
被拒、进程死亡陈旧接管、PID 复用不误认、释放他人锁被拒、令牌单调递增、
提交前/后故障注入、并发获取唯一赢家、真实跨进程 SIGKILL 崩溃恢复。
