# NVMe-oF主机NQN绑定

NVMe over Fabrics 子系统靠 Connect 命令里的 Host NQN 决定谁能访问，攻击者冒用别的主机的 NQN，或用格式不合规的 NQN 绕过白名单匹配。为Go的 NVMe-oF 目标端写可导入的 Connect 准入包：Host NQN 须是 UTF-8、不超过 223 字节，形如 `nqn.yyyy-mm.<反向域名>:<后缀>` 或 `nqn.2014-08.org.nvmexpress:uuid:<uuid>`，其余拒绝；子系统的 allowed_hosts 精确按字节比较，不做大小写折叠；每个 Host NQN 绑定首次认证时的 HostID，同一 NQN 在原连接存在时以另一个 HostID 连接则拒绝；发现日志只列出该主机被允许的子系统；已建立管理队列的控制器 ID 不得被其他主机的 IO 队列 Connect 复用。测冒用、超长 NQN、大小写变体、发现日志过滤。
