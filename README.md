# QEMU监控套接字命令授权

QEMU 的 QMP 监控套接字能发 device_add、blockdev-add、human-monitor-command 等命令，拿到套接字就等于控制虚拟机甚至宿主，攻击者借套接字文件权限、管理端口转发或绕过能力协商取得访问。为 Go 虚拟化管理服务写供 QMP 代理调用的命令授权包：输入对端凭据（套接字的 uid、gid 与权限位，SO_PEERCRED 结果）、命令 JSON（execute、arguments、id）与角色策略；先要求 qmp_capabilities 协商完成才放行其他命令；human-monitor-command 仅 admin 角色可用；getfd、add-fd 与 blockdev-add 的路径参数须限定在调用方登记的目录内；只读角色只允许 query- 前缀命令；命令名区分大小写；arguments 里 driver 为 host_device 或 file 且指向 /dev 或 /proc 拒绝；批量发送时某条被拒不影响对后续命令各自独立判定。用未协商、越权角色、/dev 路径、大小写变形和合规命令验证。
