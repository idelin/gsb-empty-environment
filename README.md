# systemd沙箱视图推导

服务单元用 ProtectSystem、ReadOnlyPaths、InaccessiblePaths、ReadWritePaths 与 BindPaths 限制进程可见的文件系统，指令顺序与路径前缀会让预期的只读保护失效，攻击者攻陷服务后借此写系统目录。为Go加固评审组件实现供部署前检查调用的沙箱视图推导包：按本题规定，指令依单元中出现的顺序依次作用于路径视图，ProtectSystem=strict 使整个根只读，full 使 /usr、/boot、/etc 只读，ReadWritePaths 在其上开洞，InaccessiblePaths 优先级最高并覆盖其所有子路径，BindPaths 的目标继承其所在视图的只读状态；给定访问路径返回读写、只读或不可见。用先开洞后只读、子路径不可见、绑定目标和 strict 开洞验证。
