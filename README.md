# NVMe-oF主机NQN绑定

NVMe over Fabrics 子系统靠 Connect 命令里的 Host NQN 决定谁能访问，攻击者冒用别的主机的 NQN，或用格式不合规的 NQN 绕过白名单匹配。为Go的 NVMe-oF 目标端写可导入的 Connect 准入包：Host NQN 须是 UTF-8、不超过 223 字节，形如 `nqn.yyyy-mm.<反向域名>:<后缀>` 或 `nqn.2014-08.org.nvmexpress:uuid:<uuid>`，其余拒绝；子系统的 allowed_hosts 精确按字节比较，不做大小写折叠；每个 Host NQN 绑定首次认证时的 HostID，同一 NQN 在原连接存在时以另一个 HostID 连接则拒绝；发现日志只列出该主机被允许的子系统；已建立管理队列的控制器 ID 不得被其他主机的 IO 队列 Connect 复用。测冒用、超长 NQN、大小写变体、发现日志过滤。

## 实现

可导入包位于 `nvmf/`（导入路径 `example.org/netsec/q0454/nvmf`），无第三方依赖。

- `nvmf.ValidateHostNQN`：UTF-8、223 字节上限、两种规范形式的严格校验，违规返回 `ErrInvalidNQN`/`ErrNQNTooLong`/`ErrNQNNotUTF8`/`ErrMalformedUUID` 等哨兵错误。
- `nvmf.NewSubsystem` / `Subsystem.AllowHost`：allowed_hosts 在写入时先过 NQN 校验；匹配通过 map 查找做精确字节比较，无大小写折叠、无 trim、无 Unicode 归一化。
- `nvmf.Authorizer.AdmitConnect`：Connect 准入主入口，顺序为 NQN 语法 → 非零 HostID → 子系统存在 → 白名单 → HostID 绑定 → 控制器 ID 归属，全部通过后才改状态。
- HostID 绑定按 NQN 记录并带连接引用计数；原连接存活期间换 HostID 返回 `ErrHostIDMismatch`，全部连接释放后允许重新绑定（`ReleaseConnection`）。
- `nvmf.Authorizer.DiscoveryLog`：仅返回白名单包含该主机的子系统，按子系统 NQN 排序，输入 NQN 不合规时返回错误且不给任何条目。
- 控制器 ID 归属按“子系统 NQN + 控制器 ID”记录在管理队列 Connect 上；其他主机的管理/IO 队列复用返回 `ErrControllerInUse`，无管理队列的 IO Connect 返回 `ErrNoAdminQueue`。

## 测试

`go test -race -cover ./nvmf/` 覆盖：NQN 冒用与 HostID 冲突、223/224 字节边界与超长拒绝、大小写/空格变体无法匹配白名单、非 UTF-8 与畸形 UUID 拒绝、发现日志按主机过滤、跨主机控制器 ID 复用拒绝、释放后重绑定以及并发安全。
