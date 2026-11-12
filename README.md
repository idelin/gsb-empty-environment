# rootless映射区间校验

rootless 容器启动器向特权辅助程序请求 uid_map 与 gid_map 时，攻击者想映射宿主 uid 0 或越出自己在 subuid 里被分配的区间。为Go启动器实现供写映射前调用的校验包：输入调用者 uid、其 subuid 与 subgid 区间表和请求的（容器起点、宿主起点、长度）列表；宿主段只能是调用者自身 uid（长度 1）或落在其区间内，容器段不得重叠，起点加长度按 uint32 计算不得回绕，条目不超过 340；写 gid_map 前须先 setgroups=deny，除非调用者已有 CAP_SETGID。用宿主 uid 0、回绕、区间边界、重叠和先写 gid_map 的顺序验证。
