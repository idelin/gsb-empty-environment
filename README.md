# IDMAP挂载越权

ID 映射挂载把共享文件系统暴露给用户命名空间，攻击者在映射范围内伪造 uid 0 创建带 setuid 或文件能力的文件，再被映射外的主机进程执行。为 Go 挂载服务写供其调用的 idmap 请求校验包：映射区间必须落在请求者自己的 uid 范围内且不重叠，起点加长度不得回绕；映射到非初始命名空间 root 的挂载必须 nosuid，security.capability 的 rootid 需随映射翻译。用重叠区间、回绕、root 映射和 xattr rootid 验证。
