# pidfd发信号授权与pid复用

pidfd_send_signal 把信号发给 pidfd 对应的进程，攻击者想拿别人遗留的 pidfd，或在 pid 被复用后继续向新进程发信号，还可借 siginfo 参数伪造 si_code。为 Go 沙箱宿主写供系统调用拦截调用的授权包：pidfd 创建时绑定目标进程的 pid 与启动时间戳，目标退出后发信号返回 ESRCH，不得落到复用同一 pid 的新进程上；发送者须与目标同用户或持有 CAP_KILL，且目标须在调用者所在 PID 命名空间可见；siginfo 非空时，向非自身进程发信号的 si_code 为非负或 SI_TKILL 一律拒绝，si_signo 须等于信号编号；flags 只允许 0。用 pid 复用、跨命名空间、伪造 si_code、同用户放行和已退出目标验证。

## 策略

- `pidfd_open` 被拦截后调用 `Registry.Bind`，绑定 `(pid, starttime)`、目标 UID 与 PID 命名空间。
- 每次 `pidfd_send_signal` 都重新读取 `/proc/<pid>/stat` 的 starttime；目标消失返回 `ESRCH`，PID 已复用也返回 `ESRCH`，且不会调用底层发送。
- 发送者 UID 与目标 UID 相同，或 `Caller.CapKill` 为真时才放行。
- 目标必须与调用者在同一 PID 命名空间，或位于 `Caller.VisiblePIDNamespaces` 列出的可见子命名空间。
- 非自身进程携带 `siginfo` 时，只允许负的 `si_code`（`SI_TKILL` 除外）；`si_signo` 必须等于信号编号。
- `flags` 必须为 0；未注册/已关闭/文件描述符被复用的 pidfd 返回 `EBADF`。

错误同时匹配包内 sentinel 和 syscall errno：

```go
errors.Is(err, netsec.ErrPIDReused) // true
errors.Is(err, syscall.ESRCH)       // true
```

## 用法

```go
h := netsec.NewOSHost()          // Linux; tests can use NewMemoryHost()
r := netsec.NewRegistry(h)

caller := netsec.Caller{
    PID:          1234,
    UID:          uint32(os.Geteuid()),
    PIDNamespace: callerPIDNamespace,
    CapKill:      false,
}

// Intercepted pidfd_open(pid) returned fd.
p, err := r.Bind(caller, fd, pid)
if err != nil {
    return err
}

// Intercepted pidfd_send_signal(fd, sig, siginfo, flags).
err = r.SendFD(caller, fd, int(syscall.SIGTERM), nil, 0)
if err != nil {
    return err
}
```

`Registry.Authorize` / `AuthorizeFD` run the same checks without delivering.
`SendSignal`, `AuthorizeSendSignal`, and `Session` are convenience wrappers.
