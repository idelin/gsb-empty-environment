# pidfd发信号授权与pid复用

pidfd_send_signal 把信号发给 pidfd 对应的进程，攻击者想拿别人遗留的 pidfd，或在 pid 被复用后继续向新进程发信号，还可借 siginfo 参数伪造 si_code。为 Go 沙箱宿主写供系统调用拦截调用的授权包：pidfd 创建时绑定目标进程的 pid 与启动时间戳，目标退出后发信号返回 ESRCH，不得落到复用同一 pid 的新进程上；发送者须与目标同用户或持有 CAP_KILL，且目标须在调用者所在 PID 命名空间可见；siginfo 非空时，向非自身进程发信号的 si_code 为非负或 SI_TKILL 一律拒绝，si_signo 须等于信号编号；flags 只允许 0。用 pid 复用、跨命名空间、伪造 si_code、同用户放行和已退出目标验证。
