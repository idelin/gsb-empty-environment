# Landlock作用域限制信号与抽象套接字

Landlock 较新的 ABI 增加了作用域限制：被限制进程只能向同域或后代域的进程发信号，也不能连接域外创建的抽象 Unix 套接字。攻击者进程想向域外守护进程发 SIGKILL，或借 fcntl 的 F_SETOWN 让信号在之后投递。为 Go 沙箱启动器写供规则集编译与信号检查模拟调用的作用域包：输入 ABI 版本、规则集是否含信号与抽象套接字作用域、进程所属域的层级、发送者与目标、投递路径（kill、tgkill、pidfd_send_signal、F_SETOWN 触发的 SIGIO 与 SIGURG）；ABI 低于 6 而清单要求作用域时拒绝启动，不降级；向祖先域或无关域发信号拒绝，向同域与后代域放行；F_SETOWN 的授权按设置时的域判定，不按投递时判定。用越域 SIGKILL、F_SETOWN 后换域、旧 ABI 和同域信号验证。
