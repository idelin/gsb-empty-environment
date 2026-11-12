# procfs调用者身份绑定

本机特权守护进程用 PID 判断调用者身份，攻击者可在进程退出后复用 PID，或把进程名设为含空格和右括号的串来误导 /proc/PID/stat 解析。写供Go守护进程认证中间件调用的 procfs 身份绑定包：通过 SO_PEERCRED 与 pidfd 取得进程，从 stat 最后一个右括号后解析启动时刻，exe 被删除或启动时刻变化都拒绝；读取结果必须与连接时记录一致，procfs 和 pidfd 通过接口注入。用伪造 comm、PID 复用、exe 被替换和合法进程验证。
