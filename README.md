# Yama ptrace范围判定

Linux 的 Yama ptrace_scope 限制进程调试同用户的其他进程，攻击者进程想附着到浏览器或 ssh-agent 读取其内存。为Go沙箱管理组件实现供 ptrace 授权模拟调用的判定包：先通过 uid 与能力的基本检查；scope 0 同 uid 即可，1 仅目标是调用者后代，或目标已用 PR_SET_PTRACER 指定调用者（或任意进程），2 仅持有 CAP_SYS_PTRACE，3 一律禁止；PTRACE_TRACEME 不受 scope 1 限制；进程树随 fork 与 exit 变化，孤儿被重新挂到 init 后不再属于原祖先。用 scope 各级、后代判定、PR_SET_PTRACER 指定和孤儿重挂验证。
