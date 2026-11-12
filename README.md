# 无SecurityManager的栈权限求交

JVM 移除 SecurityManager 后，插件宿主要自己判定调用栈上的权限，攻击者会让低权限插件经高权限库帧间接读写文件。为Go宿主实现供权限检查点调用的权限图包：栈帧带保护域，域授予 FilePermission（精确路径、/dir/*、/dir/- 三种形式，动作 read、write）；有效权限是栈上所有域授权的交集，遇到 doPrivileged 帧则截断其下方的调用者，带限定域的 doPrivileged 还要与限定域求交；/dir/* 只含直接子项，/dir/- 含全部后代，不含同前缀的兄弟目录。用混淆代理、doPrivileged 截断和通配边界验证。
