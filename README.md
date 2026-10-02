# 行级悲观锁与死锁预防器 (Go)

基于事务时间戳的死锁预防行锁管理器，纯标准库实现，保证系统全局无死锁。

## 特性

- **行级悲观锁**：共享锁（S）/ 排他锁（X），支持锁升级（S→X）、锁重入、FIFO 等待队列
- **超时机制**：`Lock()` 带超时参数，超时返回 `ErrTimeout` 并自动退出等待队列
- **死锁预防**（按事务时间戳比较，txnID 越小事务越老）：
  - **Wait-Die**（非抢占）：老事务等待年轻事务；年轻事务请求老事务持有的锁时自杀（Die）
  - **Wound-Wait**（抢占）：老事务直接抢占（Wound）年轻事务迫使其回滚重启；年轻事务等待老事务
- 两种策略均为"老/新单向裁决"，不会产生循环等待，因此**全局无死锁**

## 结构

```
lockmgr/lockmgr.go        锁管理器实现
lockmgr/lockmgr_test.go   go test 测试（含验收边界场景）
cmd/demo/main.go          运行样例
```

## API

```go
m := lockmgr.NewManager(lockmgr.WoundWait) // 或 lockmgr.WaitDie
err := m.Lock(txnID, "rowA", lockmgr.Exclusive, 500*time.Millisecond)
// err == nil            加锁成功
// err == ErrAborted     被死锁预防机制中止(Die/Wound)，应回滚并 m.Restart(txnID)
// err == ErrTimeout     等待超时
m.Unlock(txnID, "rowA") // 释放单行锁
m.Commit(txnID)         // 提交并释放全部锁
m.Restart(txnID)        // 重启被中止的事务
```

## 运行

```sh
go test ./lockmgr/ -v   # 运行测试
go run ./cmd/demo       # 运行演示
```

## 验收边界对应测试

| 场景 | 测试 |
| --- | --- |
| Wound-Wait：老事务持X锁，新事务请求 → 新事务挂起等待 | `TestWoundWait_YoungWaitsForOld` |
| Wound-Wait：新事务持锁，老事务请求 → 老事务抢占（Wound）新事务迫使其重启 | `TestWoundWait_OldWoundsYoung` |
| Wait-Die：新事务请求老事务的锁 → 自杀 | `TestWaitDie_YoungDiesForOld` |
| Wait-Die：老事务请求新事务的锁 → 等待 | `TestWaitDie_OldWaitsForYoung` |
| 锁等待超时 | `TestLockTimeout` |
| 并发竞争下无死锁/活锁 | `TestNoDeadlockUnderContention` |
