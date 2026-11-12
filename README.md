# overlayfs复制上升属主与能力保留

overlayfs 把下层文件复制到上层时保留属主与能力属性，攻击者在用户命名空间里挂载 overlay，把带 setuid 位或文件能力的下层文件复制上去，再在宿主命名空间里以 root 属主执行。为 Go 容器运行时写供挂载与复制上升模拟调用的策略包：输入下层目录项（路径、uid、gid、权限位含 setuid 与 setgid、security.capability 属性）、挂载所属用户命名空间的 uid 映射、挂载选项（metacopy、redirect_dir、userxattr、nosuid）与对文件的修改操作；映射外 uid 在上层显示为溢出 id，复制上升若会把属主写成宿主 uid 0 则拒绝；带文件能力属性的复制上升须清除属性或拒绝；metacopy 开启时仅改元数据的复制上升，其 redirect 属性须落在挂载的下层范围内；nosuid 挂载中复制上升后的 setuid 位不生效。用映射到宿主 0、带能力文件、越范围 redirect 和 nosuid 验证。
