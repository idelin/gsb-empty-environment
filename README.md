# pidfd发信号授权与pid复用

pidfd_send_signal 把信号发给 pidfd 对应的进程，攻击者想拿别人遗留的 pidfd，或在 pid 被复用后继续向新进程发信号，还可借 siginfo 参数伪造 si_code。为 Go 沙箱宿主写供系统调用拦截调用的授权包：pidfd 创建时绑定目标进程的 pid 与启动时间戳，目标退出后发信号返回 ESRCH，不得落到复用同一 pid 的新进程上；发送者须与目标同用户或持有 CAP_KILL，且目标须在调用者所在 PID 命名空间可见；siginfo 非空时，向非自身进程发信号的 si_code 为非负或 SI_TKILL 一律拒绝，si_signo 须等于信号编号；flags 只允许 0。用 pid 复用、跨命名空间、伪造 si_code、同用户放行和已退出目标验证。

## 授权策略

拦截器在 `pidfd_open` 时调用 `OpenPIDFD`，在 `pidfd_send_signal` 时调用
`SendSignal`。返回的 `syscall.Errno` 即应回填给调用者的 Linux 错误码：

- `EBADF`：pidfd 编号未知或已关闭。
- `ESRCH`：绑定进程已退出、PID 已被复用（启动时间戳不匹配），或目标在
  调用者 PID 命名空间不可见。
- `EPERM`：目标存活且可见，但 UID 不同且无 `CAP_KILL`。
- `EINVAL`：`flags` 非 0、信号号不在 `[1, 64]`、`si_signo` 与信号不一致，
  或向非自身进程使用非负 `si_code` / `SI_TKILL` 伪造来源。

pidfd 在创建时绑定 `(PID, 启动时间戳, PID 命名空间)`，每次发送都通过
`ProcessResolver` 重新查活动进程表，因此遗留或被盗的 pidfd 不能绕过常规
kill 权限，也不会命中复用同一 PID 的新进程。

## 用法

```go
auth := netsec.NewAuthorizer(hostResolver) // hostResolver 实现 netsec.ProcessResolver

fd, err := auth.OpenPIDFD(callerNS, pid)    // 拦截 pidfd_open
// ...
err = auth.SendSignal(netsec.Caller{
    UID: euid, CapKill: hasCapKill, NS: callerNS, SelfID: ownStableID,
}, fd, sig, siginfoOrNil, flags)            // 拦截 pidfd_send_signal
if errno, ok := err.(syscall.Errno); ok {
    // 拦截器返回 -1 并回填 errno；err == nil 时放行真实系统调用
}
```

宿主只需实现 `ProcessResolver`（通常基于 `/proc/[pid]/stat` 的 pid 与
starttime），包本身不发起任何真实系统调用。运行 `go test ./...` 验证。
