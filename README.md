# NFSv4 OPEN创建属性权限

NFSv4 的 OPEN 带 CREATE 时可以同时设置 owner、mode、size 属性，攻击者借此创建属于别人的文件，或用 share 拒绝位独占他人文件。为Go的 NFSv4.1 服务端写可导入的 OPEN 授权包：AUTH_SYS 凭据里的 uid 经调用方给的 root 压缩规则映射后参与判定；创建时 owner 属性只能等于映射后的调用者，特权身份除外；GUARDED 遇已存在文件返回 NFS4ERR_EXIST，EXCLUSIVE4_1 仅当 verifier 与已存文件一致才视为重试成功，不一致返回 NFS4ERR_EXIST；share_deny 与已有打开冲突返回 NFS4ERR_SHARE_DENIED，且只有对文件有读或写权限的主体才能获取 share 状态；失败的 OPEN 不得留下已创建的文件。测改 owner、verifier 重试、share 冲突、失败回滚。
