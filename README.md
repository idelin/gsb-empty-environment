# WASI文件系统描述符路径解析

宿主把预打开目录交给 WASI preview2 组件，攻击者控制的组件用 descriptor.open-at 传含 .. 的路径、绝对路径，或沿符号链接逃出预打开目录。为 Go 运行时写供 wasi:filesystem 实现调用的路径解析包：输入预打开根、各层目录项（含符号链接目标）、path-flags 是否带 symlink-follow、open-flags 与 descriptor-flags，逐段解析，.. 不得越过根，绝对路径与空段拒绝；符号链接只在 symlink-follow 时展开，展开总次数不超过 8；经只读描述符打开的新描述符不得带写权限，子描述符权限只能是父描述符权限的子集。用多级 ..、绝对路径、符号链接成环、跟随与不跟随末段链接、只读父降级验证。
