# QEMU监控套接字命令授权

QEMU 的 QMP 监控套接字能发 device_add、blockdev-add、human-monitor-command 等命令，拿到套接字就等于控制虚拟机甚至宿主，攻击者借套接字文件权限、管理端口转发或绕过能力协商取得访问。为 Go 虚拟化管理服务写供 QMP 代理调用的命令授权包：输入对端凭据（套接字 uid、gid 与权限位，SO_PEERCRED 结果）、命令 JSON（execute、arguments、id）与角色策略；每连接独立协商，未完成 qmp_capabilities 前除该命令外一律拒绝；execute 名区分大小写且须精确匹配角色白名单，human-monitor-command 仅 admin 可用；getfd、add-fd 与 blockdev-add 的路径须 realpath 后仍在调用方登记目录内，含 .. 或符号链接越界拒绝；只读角色只允许 query- 前缀；arguments 里 driver 为 host_device 或 file 且指向 /dev 或 /proc 拒绝；id 原样回显但不参与授权；批量发送时逐条独立判定，前条被拒不影响后续；拒绝须记录对端凭据与命令名。用未协商、越权角色、符号链接越界、大小写变形、批量混合与合规命令验证。
