# 进程会话终止边界

租户退出后，派生进程不能通过改组逃过清理。写供Go进程宿主调用的归属模型包，身份按创建时继承的不可变租户代次追踪，进程组与会话变化不改变归属；清理先封锁新派生，再终止整个可达后代集合并等待确认。用事件模型测试父先退出、子进程改组和进程号重用，不误杀新租户。

## 实现：`ownership` 包

事件溯源（event-sourced）的进程归属模型，平台无关的核心规则在
`ownership` 包内，Unix（darwin/linux）宿主适配层提供真实的 fork/exec
与信号投递。

- 身份：`Gen{TenantID, Epoch}` 是不可变租户代次，进程在 `Spawned` 事件
  创建时继承父进程代次；`Gen.String/ParseGen` 用于环境变量标记
  `PROCESSHOST_TENANT_GEN`，宿主的回收/领养循环据此回填代次。
- 归属不变量：`GroupChanged`（setpgid/setsid）只更新 PGID/SID 元数据，
  不改变代次；父进程先退出后，孤儿子进程仍保留出生代次，
  `ReachableFrom` 沿记录的父子边仍能找到它。
- PID 重用：存活进程占用同一 PID 时拒绝派生（`ErrPIDInUse`）；已退出
  PID 被新租户重用时以新代次重建身份，旧代次清理只按代次成员集发信号，
  不会触达新租户进程。
- 清理协议（`Shutdown`）：
  1. `Sealed` 封存代次，之后该代次的 `Spawned` 一律返回
     `ErrGenerationSealed`，先封锁新派生；
  2. 对封存时刻的整个可达后代集（`LiveMembers`）发 SIGTERM；
  3. 等待 `Exited` 确认，宽限期后对存活者升级 SIGKILL 并循环等待；
  4. 全部退出返回 nil；上下文超时返回带存活 PID 列表的 `ShutdownError`。
- 事件与测试：所有状态变化为 `Spawned/Exited/GroupChanged/Sealed` 事件，
  测试用内存 `fakeKiller` 确定性驱动，覆盖父先退出、子进程改组/新会话、
  PID 重用不误杀新租户、SIGKILL 升级、超时与封存竞态。

## 运行

```sh
go test ./... -race -count=1
```

宿主集成入口见 `ownership/host_unix.go` 的 `Host`（`SpawnRoot`、
`ObserveSpawn`、`ObserveExit`、`ObserveGroupChanged`、`CleanupTenant`），
完整调用示例见 `ownership/example_test.go`。
