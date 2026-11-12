# SFTP扩展请求权限

SFTP 服务器对基本操作做了只读和路径限制，攻击者改走 SSH_FXP_EXTENDED 里的 posix-rename@openssh.com、hardlink@openssh.com、copy-data 或 fsetstat@openssh.com 绕过。为Go的 SFTP 服务端写可导入的请求授权包：每个扩展请求先映射为等价的基本权限（重命名需源目录与目标目录写权限，硬链接需源读权限与目标目录写权限，copy-data 需源读与目标写）再走同一套路径规范化与只读开关；未知扩展名一律返回 SSH_FX_OP_UNSUPPORTED，不因扩展名带 @ 域名而放行；规范化后越出根目录即拒绝，符号链接解析后的真实路径也须在根内（文件系统接口由调用方注入）。测只读模式下的各扩展、越根重命名、硬链接到无读权限的文件。
