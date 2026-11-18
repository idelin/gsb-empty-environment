# Tetragon跟踪策略同族调用覆盖

Tetragon 的跟踪策略在内核函数上挂钩，按参数选择器放行或终止进程，攻击者改用同族的其他系统调用（openat2 代替 open）、相对路径或带 .. 的路径，绕过只钩住某一个调用的策略。为 Go 策略下发服务写供策略保存前调用的覆盖校验包：输入策略的钩子（调用名、参数类型、选择器：Prefix、Equal、Postfix，动作 Sigkill 或 Override）、组织要求保护的目标（路径与访问方式）和同族调用表（open、openat、openat2、creat、truncate、rename、renameat2 等）；同族表须覆盖 openat2 的 dirfd 相对解析与 RESOLVE_* 限制；Prefix 只对规范化后路径有效，相对路径须标注不受保护；Postfix 不能单独用于目录保护；Override 缺返回码或返回 0 拒绝；报告某保护目标经哪个同族调用仍未被覆盖，同一目标有任一未覆盖路径即报缺口并给出覆盖矩阵。用只钩 openat、openat2 变体、相对路径、仅 Postfix、Override 0 与完整覆盖验证。
