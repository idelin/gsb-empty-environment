# SFTP扩展请求权限

SFTP 服务器对基本操作做了只读和路径限制，攻击者改走 SSH_FXP_EXTENDED 里的 posix-rename@openssh.com、hardlink@openssh.com、copy-data 或 fsetstat@openssh.com 绕过。为Go的 SFTP 服务端写可导入的请求授权包：每个扩展请求先映射为等价的基本权限（重命名需源目录与目标目录写权限，硬链接需源读权限与目标目录写权限，copy-data 需源读与目标写）再走同一套路径规范化与只读开关；路径逐段规范化，符号链接在每一级解析且真实路径须在根内，大小写按注入文件系统规则；posix-rename 跨目录须同设备且不得覆盖只读文件，copy-data 区间不得越界或绕过只读，fsetstat 不得改变只读属性；未知扩展名一律返回 SSH_FX_OP_UNSUPPORTED，不因带 @ 域名而放行；拒绝须返回精确状态码。测只读下各扩展、跨目录重命名、符号链接越界、copy-data 越界与 fsetstat 改权限。
